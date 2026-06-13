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

	"github.com/andrey-vk/wdbgp/internal/retry"
	"github.com/andrey-vk/wdbgp/internal/store"
	"github.com/dop251/goja"
)



type adapterRunner struct {
	client  *http.Client
	timeout time.Duration
	limits  AdapterLimits
}

func ValidateAdapterSource(source string, maxBytes int) error {
	if len(source) > maxBytes {
		return fmt.Errorf("adapter source exceeds %d bytes", maxBytes)
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
	if err := ValidateAdapterSource(adapter.Source, r.limits.MaxSourceBytes); err != nil {
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
	vm.SetMaxCallStackSize(r.limits.MaxCallStack)
	program, err := goja.Compile(adapter.Key+".js", adapter.Source, true)
	if err != nil {
		return nil, fmt.Errorf("compile adapter: %w", err)
	}
	executionDone := make(chan struct{})
	defer close(executionDone)
	go func() {
		select {
		case <-runCtx.Done():
			vm.Interrupt(runCtx.Err())
		case <-executionDone:
		}
	}()
	if _, err := vm.RunProgram(program); err != nil {
		return nil, fmt.Errorf("initialize adapter: %w", err)
	}
	syncFunction, ok := goja.AssertFunction(vm.Get("sync"))
	if !ok {
		return nil, fmt.Errorf("adapter must define function sync(feed, api)")
	}

	httpAPI, err := newAdapterHTTP(runCtx, r.client, feed.URL, adapter.AllowedHosts, r.limits)
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
	if len(rawEntries) > r.limits.MaxEntries {
		return nil, fmt.Errorf("adapter returned more than %d entries", r.limits.MaxEntries)
	}
	return normalizeCanonical(rawEntries, r.limits.MaxEntries)
}

func normalizeCanonical(rawEntries []canonicalEntry, maxEntries int) ([]Entry, error) {
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
			if len(entries) > maxEntries {
				return nil, fmt.Errorf("adapter returned more than %d CIDRs", maxEntries)
			}
		}
	}
	return deduplicate(entries), nil
}

type adapterHTTP struct {
	ctx          context.Context
	client       *http.Client
	allowedHosts map[string]bool
	limits       AdapterLimits
	requests     int
	totalBytes   int64
}

func newAdapterHTTP(
	ctx context.Context,
	client *http.Client,
	feedURL string,
	additionalHosts string,
	limits AdapterLimits,
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
		ctx: ctx, client: client, allowedHosts: allowed, limits: limits,
	}, nil
}

func (a *adapterHTTP) get(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("adapter HTTP URL must be absolute HTTP or HTTPS")
	}
	if err := a.validateURL(parsed); err != nil {
		return "", err
	}
	
	// Use retry with exponential backoff for HTTP requests
	result, err := retry.DoWithResult(a.ctx, retry.HTTPConfig, 
		func() (string, error) {
			return a.doHTTPRequest(parsed)
		},
		retry.HTTPTransientError,
	)
	
	if err != nil {
		// Check if it's a context error
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("adapter HTTP request timeout: %w", err)
		}
		return "", fmt.Errorf("adapter HTTP request failed: %w", err)
	}
	
	return result, nil
}

func (a *adapterHTTP) doHTTPRequest(parsed *url.URL) (string, error) {
	a.requests++

	request, err := http.NewRequestWithContext(a.ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "wdbgp-go/1.0")
	
	// Create a copy of the client with custom redirect handling
	client := *a.client
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if err := a.validateURL(request.URL); err != nil {
			return err
		}
		a.requests++
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		return nil
	}
	
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %s", response.Status)
	}
	
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(a.limits.MaxResponseBytes)+1))
	if err != nil {
		return "", err
	}

	if len(body) > a.limits.MaxResponseBytes {
		return "", fmt.Errorf("adapter HTTP response exceeds %d bytes", a.limits.MaxResponseBytes)
	}

	a.totalBytes += int64(len(body))
	if a.totalBytes > int64(a.limits.MaxTotalBytes) {
		return "", fmt.Errorf("adapter HTTP responses exceed %d bytes", a.limits.MaxTotalBytes)
	}
	
	return string(body), nil
}

func (a *adapterHTTP) validateURL(parsed *url.URL) error {
	host := strings.ToLower(parsed.Hostname())
	if !a.allowedHosts[host] {
		return fmt.Errorf("adapter HTTP host %q is not allowed", host)
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if !isPublicAddress(address) {
			return fmt.Errorf("adapter HTTP address %q is not public", host)
		}
		return a.reserveRequest()
	}
	if transport, ok := a.client.Transport.(*http.Transport); ok && transport.Proxy != nil {
		request := &http.Request{URL: parsed}
		proxyURL, err := transport.Proxy(request)
		if err != nil {
			return err
		}
		if proxyURL != nil {
			addresses, err := net.DefaultResolver.LookupNetIP(a.ctx, "ip", host)
			if err != nil {
				return err
			}
			if !hasPublicAddress(addresses) {
				return fmt.Errorf("host %q has no public addresses", host)
			}
		}
	}
	return a.reserveRequest()
}

func (a *adapterHTTP) reserveRequest() error {
	if a.requests >= a.limits.MaxRequests {
		return fmt.Errorf("adapter exceeded %d HTTP requests", a.limits.MaxRequests)
	}
	return nil
}

func newHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	proxy := http.ProxyFromEnvironment
	transport := &http.Transport{
		Proxy: proxy,
		DialContext: publicDialContext(
			net.DefaultResolver.LookupNetIP,
			dialer.DialContext,
			proxyHosts(proxy),
		),
		ForceAttemptHTTP2: true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

type lookupNetIPFunc func(context.Context, string, string) ([]netip.Addr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

func publicDialContext(
	lookup lookupNetIPFunc,
	dial dialContextFunc,
	proxies map[string]bool,
) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if proxies[strings.ToLower(host)] {
			return dial(ctx, network, address)
		}
		addresses, err := lookup(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		var dialErrors []error
		for _, resolved := range addresses {
			if !isPublicAddress(resolved) {
				continue
			}
			connection, err := dial(ctx, network,
				net.JoinHostPort(resolved.String(), port))
			if err == nil {
				return connection, nil
			}
			dialErrors = append(dialErrors, err)
		}
		if len(dialErrors) > 0 {
			return nil, errors.Join(dialErrors...)
		}
		return nil, fmt.Errorf("host %q has no public addresses", host)
	}
}

func proxyHosts(proxy func(*http.Request) (*url.URL, error)) map[string]bool {
	hosts := map[string]bool{}
	for _, rawURL := range []string{"http://example.com", "https://example.com"} {
		request, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			continue
		}
		proxyURL, err := proxy(request)
		if err == nil && proxyURL != nil && proxyURL.Hostname() != "" {
			hosts[strings.ToLower(proxyURL.Hostname())] = true
		}
	}
	return hosts
}

func hasPublicAddress(addresses []netip.Addr) bool {
	for _, address := range addresses {
		if isPublicAddress(address) {
			return true
		}
	}
	return false
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
