package feeds

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/store"
	"github.com/dop251/goja"
)

const (
	maxAdapterRequests      = 200
	maxAdapterResponseBytes = 16 << 20
	maxAdapterTotalBytes    = 64 << 20
	maxAdapterEntries       = 1_000_000
	maxAdapterSourceBytes   = 1 << 20
)

type adapterRunner struct {
	client  *http.Client
	timeout time.Duration
}

func ValidateAdapterSource(source string) error {
	if len(source) > maxAdapterSourceBytes {
		return fmt.Errorf("adapter source exceeds %d bytes", maxAdapterSourceBytes)
	}
	_, err := goja.Compile("feed-adapter.js", source, true)
	if err != nil {
		return fmt.Errorf("compile adapter: %w", err)
	}
	return nil
}

func FormatAdapterError(err error) string {
	var exception *goja.Exception
	if errors.As(err, &exception) {
		return exception.String()
	}
	return err.Error()
}

func (r adapterRunner) run(
	ctx context.Context,
	feed store.Feed,
	adapter store.FeedAdapter,
) ([]Entry, error) {
	if adapter.APIVersion != 1 {
		return nil, fmt.Errorf("unsupported adapter API version %d", adapter.APIVersion)
	}
	if err := ValidateAdapterSource(adapter.Source); err != nil {
		return nil, err
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	vm.SetMaxCallStackSize(1_000)
	program, err := goja.Compile(adapter.Key+".js", adapter.Source, true)
	if err != nil {
		return nil, fmt.Errorf("compile adapter: %w", err)
	}
	timer := time.AfterFunc(timeout, func() {
		vm.Interrupt("adapter execution timed out")
	})
	defer timer.Stop()
	if _, err := vm.RunProgram(program); err != nil {
		return nil, fmt.Errorf("initialize adapter: %w", err)
	}
	syncFunction, ok := goja.AssertFunction(vm.Get("sync"))
	if !ok {
		return nil, fmt.Errorf("adapter must define function sync(feed, api)")
	}

	httpAPI, err := newAdapterHTTP(runCtx, r.client, feed.URL, adapter.AllowedHosts)
	if err != nil {
		return nil, err
	}
	api := vm.NewObject()
	if err := api.Set("httpGet", func(call goja.FunctionCall) goja.Value {
		body, requestErr := httpAPI.get(call.Argument(0).String())
		if requestErr != nil {
			panic(vm.NewGoError(requestErr))
		}
		return vm.ToValue(body)
	}); err != nil {
		return nil, err
	}
	feedValue := map[string]any{
		"id":   feed.ID,
		"name": feed.Name,
		"url":  feed.URL,
	}
	value, err := syncFunction(
		goja.Undefined(),
		vm.ToValue(feedValue),
		api,
	)
	if err != nil {
		if runCtx.Err() != nil {
			return nil, fmt.Errorf("execute adapter: %w", runCtx.Err())
		}
		return nil, fmt.Errorf("execute adapter: %w", err)
	}
	var rawEntries []canonicalEntry
	if err := vm.ExportTo(value, &rawEntries); err != nil {
		return nil, fmt.Errorf("adapter result must be an entry array: %w", err)
	}
	if len(rawEntries) > maxAdapterEntries {
		return nil, fmt.Errorf("adapter returned more than %d entries", maxAdapterEntries)
	}
	return normalizeCanonical(rawEntries)
}

func normalizeCanonical(rawEntries []canonicalEntry) ([]Entry, error) {
	var entries []Entry
	for _, value := range rawEntries {
		value.Category = strings.TrimSpace(value.Category)
		value.Service = strings.TrimSpace(value.Service)
		if value.Category == "" || value.Service == "" || value.CIDRs == nil {
			return nil, fmt.Errorf("each entry requires category, service and cidrs[]")
		}
		for _, rawCIDR := range value.CIDRs {
			cidr, err := store.NormalizePrefix(rawCIDR)
			if err != nil {
				return nil, fmt.Errorf("%s/%s CIDR %q: %w",
					value.Category, value.Service, rawCIDR, err)
			}
			entries = append(entries, Entry{
				Category: value.Category,
				Service:  value.Service,
				CIDR:     cidr,
			})
			if len(entries) > maxAdapterEntries {
				return nil, fmt.Errorf("adapter returned more than %d CIDRs", maxAdapterEntries)
			}
		}
	}
	return deduplicate(entries), nil
}

type adapterHTTP struct {
	ctx          context.Context
	client       *http.Client
	allowedHosts map[string]bool
	requests     int
	totalBytes   int64
}

func newAdapterHTTP(
	ctx context.Context,
	client *http.Client,
	feedURL string,
	additionalHosts string,
) (*adapterHTTP, error) {
	parsedFeedURL, err := url.Parse(feedURL)
	if err != nil || parsedFeedURL.Hostname() == "" {
		return nil, fmt.Errorf("invalid feed URL")
	}
	allowed := map[string]bool{strings.ToLower(parsedFeedURL.Hostname()): true}
	for _, host := range strings.FieldsFunc(additionalHosts, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		allowed[strings.ToLower(strings.TrimSpace(host))] = true
	}
	return &adapterHTTP{
		ctx: ctx, client: client, allowedHosts: allowed,
	}, nil
}

func (a *adapterHTTP) get(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("adapter HTTP URL must be absolute HTTP or HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if !a.allowedHosts[host] {
		return "", fmt.Errorf("adapter HTTP host %q is not allowed", host)
	}
	if address, err := netip.ParseAddr(host); err == nil && !isPublicAddress(address) {
		return "", fmt.Errorf("adapter HTTP address %q is not public", host)
	}
	if a.requests >= maxAdapterRequests {
		return "", fmt.Errorf("adapter exceeded %d HTTP requests", maxAdapterRequests)
	}
	a.requests++

	request, err := http.NewRequestWithContext(a.ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "wdbgp-go/1.0")
	response, err := a.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAdapterResponseBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxAdapterResponseBytes {
		return "", fmt.Errorf("adapter HTTP response exceeds %d bytes", maxAdapterResponseBytes)
	}
	a.totalBytes += int64(len(body))
	if a.totalBytes > maxAdapterTotalBytes {
		return "", fmt.Errorf("adapter HTTP responses exceed %d bytes", maxAdapterTotalBytes)
	}
	return string(body), nil
}

func newHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, address := range addresses {
				if !isPublicAddress(address) {
					continue
				}
				return dialer.DialContext(ctx, network,
					net.JoinHostPort(address.String(), port))
			}
			return nil, fmt.Errorf("host %q has no public addresses", host)
		},
		ForceAttemptHTTP2: true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func isPublicAddress(address netip.Addr) bool {
	return address.IsValid() &&
		!address.IsLoopback() &&
		!address.IsPrivate() &&
		!address.IsLinkLocalUnicast() &&
		!address.IsLinkLocalMulticast() &&
		!address.IsMulticast() &&
		!address.IsUnspecified()
}
