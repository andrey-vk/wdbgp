package web

import (
	"context"
	"html/template"
	"net/http"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type BGP interface {
	Reconcile(context.Context) error
	ReloadPeers(context.Context) error
	PeerStates(context.Context) (map[string]string, error)
	AddPeer(context.Context, store.User) error
	UpdatePeer(context.Context, store.User) error
	DeletePeer(context.Context, string, int64) error
}

type Server struct {
	cfg          config.Config
	store        *store.Store
	syncer       *feeds.Syncer
	bgp          BGP
	defaultLang  locale
	templates    map[locale]map[string]*template.Template
	handler      http.Handler
	loginLimiter *rateLimiter
	adminLimiter *rateLimiter
	startTime    time.Time
	degraded     bool
	degradedInfo DegradedInfo
}

// DegradedInfo carries version mismatch details for the degraded-mode page.
type DegradedInfo struct {
	CurrentVersion int
	ServerVersion  int
	Reason         string // why degraded (e.g. "no backup found")
}

type categoryView struct {
	Name          string
	Selected      bool
	Services      []serviceView
	PrefixCountV4 int
	PrefixCountV6 int
}

type serviceView struct {
	Name          string
	Value         string
	Selected      bool
	Disabled      bool
	PrefixCountV4 int
	PrefixCountV6 int
}

type selectionView struct {
	User                    store.User
	Modes                   []store.CatalogMode
	CanChangeMode           bool
	Categories              []categoryView
	Editable                bool
	Admin                   bool
	Saved                   string
	SessionUser             bool // true if user authenticated via session (has logout option)
	Filters                 filterView
	SelectedCategoryCount   int
	SelectedCoveredServices int
	SelectedServiceCount    int
	CSRFToken               string
	Communities             map[string]uint32
	PrefixCountsV4          map[string]map[string]int // category -> service -> v4 count
	PrefixCountsV6          map[string]map[string]int // category -> service -> v6 count
	CategoryCountsV4        map[string]int            // category -> total unique v4 prefixes
	CategoryCountsV6        map[string]int            // category -> total unique v6 prefixes
	TotalPrefixesV4         int                       // total unique IPv4 prefixes for selection
	TotalPrefixesV6         int                       // total unique IPv6 prefixes for selection
}

type adapterTestView struct {
	Adapter      store.FeedAdapter
	Feed         store.Feed
	Entries      []feeds.Entry
	TotalEntries int
	Truncated    bool
}

type adapterEditView struct {
	Adapter store.FeedAdapter
	Feeds   []store.Feed
	Error   string
}

type communitiesView struct {
	Modes  []store.CatalogMode
	Mode   store.CatalogMode
	Groups []communityGroupView
	Error  string
	Saved  string
}

type communityGroupView struct {
	Category  string
	Community uint32
	AutoGroup uint32
	Services  []communityServiceView
}

type communityServiceView struct {
	Name      string
	Community uint32
	AutoSvc   uint32
}

type modeOption struct {
	Value    string
	Text     string
	Selected bool
}

type userEditView struct {
	User               store.User
	Selection          selectionView
	Credentials        []store.UserCredential
	Error              string
	DynamicReadonly    bool   // true when AllowDynamicPeers==false
	DynamicChecked     bool   // true when User.PeerIP is 0.0.0.0 or ::
	PasswordDisabled   bool   // true when PeerIP is wildcard (0.0.0.0 or ::)
	PasswordHint       string // tooltip hint for password field
	ActiveDial         bool   // true when User.ActiveDial (active BGP dialing enabled)
	ActiveDialDisabled bool   // true when system-wide ActiveDial==false
	ActiveDialHint     string // explanatory text when disabled
	// Computed attribute strings for form components
	PeerIPAttrs            template.HTMLAttr
	DynamicIPAttrs         template.HTMLAttr
	ActiveDialAttrs        template.HTMLAttr
	PasswordAttrs          template.HTMLAttr
	ActiveDialHintResolved string
	NetworksStr            string
	WebAuthOptions         []modeOption
	ModeOptions            []modeOption
}

type filterView struct {
	AllowText string
	DenyText  string
	Override  bool
	Mode      string
	Editable  bool
	Admin     bool
}

type globalFiltersView struct {
	Allow string // newline-separated CIDRs
	Deny  string // newline-separated CIDRs
}
