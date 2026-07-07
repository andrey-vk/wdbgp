package feeds

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/retry"
	"github.com/andrey-vk/wdbgp/internal/store"
	"github.com/dop251/goja"
)

type feedRunner struct {
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

func (r feedRunner) run(
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
	program, err := goja.Compile(fmt.Sprintf("adapter-%d.js", adapter.ID), adapter.Source, true)
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

	httpAPI, err := newAdapterRunner(runCtx, r.client, feed.URL, feed.AllowedHosts, feed.RestrictHosts, r.limits)
	if err != nil {
		return nil, err
	}
	api := vm.NewObject()
	if err := api.Set("httpGet", func(call goja.FunctionCall) goja.Value {
		body, requestErr := httpAPI.get(call.Argument(0).String())
		if requestErr != nil {
			// Panic propagates to JavaScript as a catchable exception — documented goja interop pattern.
			panic(vm.NewGoError(requestErr))
		}
		return vm.ToValue(body)
	}); err != nil {
		return nil, err
	}
	if err := api.Set("srsGet", func(call goja.FunctionCall) goja.Value {
		rawURL := call.Argument(0).String()
		cfgJSON := ""
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			cfgJSON = call.Argument(1).String()
		}
		body, requestErr := httpAPI.getBytes(rawURL)
		if requestErr != nil {
			// Panic propagates to JavaScript as a catchable exception — documented goja interop pattern.
			panic(vm.NewGoError(requestErr))
		}
		entries, parseErr := ParseSRS(runCtx, body, cfgJSON)
		if parseErr != nil {
			// Panic propagates to JavaScript as a catchable exception — documented goja interop pattern.
			panic(vm.NewGoError(parseErr))
		}
		return vm.ToValue(entries)
	}); err != nil {
		return nil, err
	}
	if err := api.Set("log", func(call goja.FunctionCall) goja.Value {
		msg := call.Argument(0).String()
		logging.FromContext(runCtx).Info("adapter log",
			"feed", feed.Name,
			"adapter", adapter.Name,
			"message", msg,
		)
		return goja.Undefined()
	}); err != nil {
		return nil, err
	}
	feedValue := map[string]any{
		"id":   feed.ID,
		"name": feed.Name,
		"url":  feed.URL,
		"data": feed.Data,
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

type adapterRunner struct {
	ctx          context.Context
	client       *http.Client
	allowedHosts map[string]bool
	limits       AdapterLimits
	requests     int
	totalBytes   int64
}

func newAdapterRunner(
	ctx context.Context,
	client *http.Client,
	feedURL string,
	additionalHosts string,
	restrictHosts bool,
	limits AdapterLimits,
) (*adapterRunner, error) {
	parsedFeedURL, err := url.Parse(feedURL)
	if err != nil || parsedFeedURL.Hostname() == "" {
		return nil, fmt.Errorf("invalid feed URL")
	}
	if !restrictHosts {
		return &adapterRunner{
			ctx: ctx, client: client, allowedHosts: nil, limits: limits,
		}, nil
	}
	allowed := map[string]bool{strings.ToLower(parsedFeedURL.Hostname()): true}
	for _, host := range strings.FieldsFunc(additionalHosts, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		allowed[strings.ToLower(strings.TrimSpace(host))] = true
	}
	return &adapterRunner{
		ctx: ctx, client: client, allowedHosts: allowed, limits: limits,
	}, nil
}

func (a *adapterRunner) get(rawURL string) (string, error) {
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
		func() ([]byte, error) {
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

	return string(result), nil
}

func (a *adapterRunner) getBytes(rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("adapter HTTP URL must be absolute HTTP or HTTPS")
	}
	if err := a.validateURL(parsed); err != nil {
		return nil, err
	}

	result, err := retry.DoWithResult(a.ctx, retry.HTTPConfig,
		func() ([]byte, error) {
			return a.doHTTPRequest(parsed)
		},
		retry.HTTPTransientError,
	)

	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("adapter HTTP request timeout: %w", err)
		}
		return nil, fmt.Errorf("adapter HTTP request failed: %w", err)
	}

	return result, nil
}

func (a *adapterRunner) doHTTPRequest(parsed *url.URL) ([]byte, error) {
	if a.requests >= a.limits.MaxRequests {
		return nil, fmt.Errorf("adapter exceeded %d HTTP requests", a.limits.MaxRequests)
	}
	a.requests++

	request, err := http.NewRequestWithContext(a.ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Printf("DEBUG: close response body: %v", err)
		}
	}()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, int64(a.limits.MaxResponseBytes)+1))
	if err != nil {
		return nil, err
	}

	if len(body) > a.limits.MaxResponseBytes {
		return nil, fmt.Errorf("adapter HTTP response exceeds %d bytes", a.limits.MaxResponseBytes)
	}

	a.totalBytes += int64(len(body))
	if a.totalBytes > int64(a.limits.MaxTotalBytes) {
		return nil, fmt.Errorf("adapter HTTP responses exceed %d bytes", a.limits.MaxTotalBytes)
	}

	return body, nil
}

func (a *adapterRunner) validateURL(parsed *url.URL) error {
	host := strings.ToLower(parsed.Hostname())
	if a.allowedHosts != nil && !a.allowedHosts[host] {
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

func (a *adapterRunner) reserveRequest() error {
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

// nonGlobalPrefixes are the IANA special-purpose ranges that are not
// globally reachable but slip past the stdlib's boolean predicates
// (IsPrivate, IsLoopback, …): CGNAT, benchmarking, documentation, reserved,
// NAT64/discard-only and friends. Addresses are checked after Unmap(), so
// IPv4 entries also cover their ::ffff:0:0/96 mapped forms.
var nonGlobalPrefixes = []netip.Prefix{
	// IPv4 (per iana-ipv4-special-registry)
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT (RFC 6598)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("192.88.99.0/24"),  // deprecated 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking (RFC 2544)
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. 255.255.255.255
	// IPv6 (per iana-ipv6-special-registry)
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("100::/63"),       // discard-only (100::/64) + dummy prefix (100:0:0:1::/64)
	netip.MustParsePrefix("2001::/23"),      // IETF: TEREDO, ORCHID, benchmarking
	netip.MustParsePrefix("2001:db8::/32"),  // documentation
	netip.MustParsePrefix("2002::/16"),      // 6to4
	netip.MustParsePrefix("3fff::/20"),      // documentation (RFC 9637)
	netip.MustParsePrefix("5f00::/16"),      // SRv6 SIDs (RFC 9602)
}

// globalExceptionPrefixes are the sub-ranges of nonGlobalPrefixes that the
// IANA special-purpose registries mark "globally reachable: True" — anycast
// and infrastructure services that legitimately live inside otherwise
// non-global blocks. Checked before the deny-list.
var globalExceptionPrefixes = []netip.Prefix{
	netip.MustParsePrefix("192.0.0.9/32"),    // PCP anycast (RFC 7723)
	netip.MustParsePrefix("192.0.0.10/32"),   // NAT traversal anycast (RFC 8155)
	netip.MustParsePrefix("2001:1::1/128"),   // PCP anycast (RFC 7723)
	netip.MustParsePrefix("2001:1::2/128"),   // NAT traversal anycast (RFC 8155)
	netip.MustParsePrefix("2001:1::3/128"),   // DNS-SD service registration (RFC 9665)
	netip.MustParsePrefix("2001:3::/32"),     // AMT (RFC 7450)
	netip.MustParsePrefix("2001:4:112::/48"), // AS112-v6 (RFC 7534)
	netip.MustParsePrefix("2001:30::/28"),    // Drone Remote ID (RFC 9374)
}

// wellKnownNAT64 is the RFC 6052 NAT64 translation prefix. DNS64 synthesizes
// these on legitimate IPv6-only networks, so it can't be denied outright —
// instead the IPv4 address embedded in the low 32 bits is validated, closing
// the hole where 64:ff9b::<internal-v4> would reach private IPv4 space
// through the site's NAT64 gateway.
var wellKnownNAT64 = netip.MustParsePrefix("64:ff9b::/96")

func isPublicAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if address.IsLoopback() ||
		address.IsPrivate() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified() {
		return false
	}
	for _, prefix := range globalExceptionPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	for _, prefix := range nonGlobalPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	if wellKnownNAT64.Contains(address) {
		raw := address.As16()
		embedded := netip.AddrFrom4([4]byte(raw[12:16]))
		return isPublicAddress(embedded)
	}
	return true
}
