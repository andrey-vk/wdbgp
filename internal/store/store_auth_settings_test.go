package store

import (
	"context"
	"testing"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Migration 14 — web_auth column + user_credentials table
// =============================================================================

func TestMigration14AddsWebAuthAndUserCredentials(t *testing.T) {
	s := openTestStore(t)

	// Verify web_auth column exists with default 'network'
	var colType, colDefault string
	var colNullable int
	err := s.DB.QueryRow(`SELECT type, "notnull", dflt_value FROM pragma_table_info('users') WHERE name = 'web_auth'`).
		Scan(&colType, &colNullable, &colDefault)
	if err != nil {
		t.Fatal("web_auth column missing:", err)
	}
	if colType != "TEXT" || colNullable != 1 || colDefault != "'network'" {
		t.Fatalf("web_auth: type=%s null=%d default=%s, want TEXT/1/'network'", colType, colNullable, colDefault)
	}

	// Add a user and verify web_auth default is 'network'
	userID, err := s.AddUser(context.Background(), User{
		Name: "default-auth", PeerIP: "10.0.0.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.User(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.WebAuth != "network" {
		t.Fatalf("default web_auth = %q, want network", user.WebAuth)
	}

	// Verify user_credentials table exists
	var tblName string
	err = s.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='user_credentials'").Scan(&tblName)
	if err != nil {
		t.Fatal("user_credentials table missing:", err)
	}

	// Verify user_credentials schema
	columns := map[string]string{}
	rows, err := s.DB.Query("SELECT name, type FROM pragma_table_info('user_credentials')")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, colType string
		if err := rows.Scan(&name, &colType); err != nil {
			t.Fatal(err)
		}
		columns[name] = colType
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close() //nolint:errcheck // test cleanup

	if columns["user_id"] != "INTEGER" {
		t.Fatalf("user_credentials.user_id type = %s, want INTEGER", columns["user_id"])
	}
	if columns["login"] != "TEXT" {
		t.Fatalf("user_credentials.login type = %s, want TEXT", columns["login"])
	}
	if columns["password_hash"] != "TEXT" {
		t.Fatalf("user_credentials.password_hash type = %s, want TEXT", columns["password_hash"])
	}

	// Verify unique index on login
	var unique int
	err = s.DB.QueryRow("SELECT 1 FROM sqlite_master WHERE type='index' AND name='idx_user_credentials_login_unique' AND sql LIKE '%UNIQUE%'").Scan(&unique)
	if err != nil {
		t.Fatal("idx_user_credentials_login_unique missing:", err)
	}
}

// =============================================================================
// Migration 15 — app_settings table
// =============================================================================

func TestMigration15AddsAppSettings(t *testing.T) {
	s := openTestStore(t)

	var tblName string
	err := s.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='app_settings'").Scan(&tblName)
	if err != nil {
		t.Fatal("app_settings table missing:", err)
	}

	// Verify schema
	columns := map[string]string{}
	rows, err := s.DB.Query("SELECT name, type FROM pragma_table_info('app_settings')")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, colType string
		if err := rows.Scan(&name, &colType); err != nil {
			t.Fatal(err)
		}
		columns[name] = colType
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close() //nolint:errcheck // test cleanup

	if columns["key"] != "TEXT" {
		t.Fatalf("app_settings.key type = %s, want TEXT", columns["key"])
	}
	if columns["value"] != "TEXT" {
		t.Fatalf("app_settings.value type = %s, want TEXT", columns["value"])
	}
	if columns["updated_at"] != "TEXT" {
		t.Fatalf("app_settings.updated_at type = %s, want TEXT", columns["updated_at"])
	}
}

// =============================================================================
// AuthenticateUser
// =============================================================================

func TestAuthenticateUserValidCredentials(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "login-user", PeerIP: "10.0.0.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "login",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set credentials
	err = s.SetUserCredential(ctx, userID, "testuser", "secret123")
	if err != nil {
		t.Fatal(err)
	}

	// Authenticate
	user, err := s.AuthenticateUser(ctx, "testuser", "secret123")
	if err != nil {
		t.Fatalf("AuthenticateUser failed: %v", err)
	}
	if user.ID != userID {
		t.Fatalf("authenticated user ID = %d, want %d", user.ID, userID)
	}
	if user.WebAuth != "login" {
		t.Fatalf("authenticated user web_auth = %q, want login", user.WebAuth)
	}
}

func TestAuthenticateUserWrongPassword(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "login-user2", PeerIP: "10.0.0.2", PeerASN: 65002, Enabled: true,
		WebAuth:  "login",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.SetUserCredential(ctx, userID, "testuser2", "correct")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AuthenticateUser(ctx, "testuser2", "wrong")
	if err == nil {
		t.Fatal("AuthenticateUser with wrong password should have failed")
	}
}

func TestAuthenticateUserDisabledUser(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "disabled-login-user", PeerIP: "10.0.0.3", PeerASN: 65003, Enabled: true,
		WebAuth:  "login",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.SetUserCredential(ctx, userID, "disabled", "secret")
	if err != nil {
		t.Fatal(err)
	}

	// Disable user
	_, err = s.DB.Exec("UPDATE users SET enabled = 0 WHERE id = ?", userID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AuthenticateUser(ctx, "disabled", "secret")
	if err == nil {
		t.Fatal("AuthenticateUser with disabled user should have failed")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected not-found error for disabled user, got: %v", err)
	}
}

func TestAuthenticateUserNonexistent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.AuthenticateUser(ctx, "nonexistent", "password")
	if err == nil {
		t.Fatal("AuthenticateUser with nonexistent user should have failed")
	}
}

// =============================================================================
// GetUserCredentials
// =============================================================================

func TestGetUserCredentialsEmpty(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "no-creds", PeerIP: "10.0.1.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"10.0.1.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	creds, err := s.GetUserCredentials(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 0 {
		t.Fatalf("credentials = %d, want 0", len(creds))
	}
}

func TestGetUserCredentialsOneAndMultiple(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "multi-creds", PeerIP: "10.0.2.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"10.0.2.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add one
	err = s.SetUserCredential(ctx, userID, "alice", "pass1")
	if err != nil {
		t.Fatal(err)
	}
	creds, err := s.GetUserCredentials(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 || creds[0].Login != "alice" || creds[0].UserID != userID {
		t.Fatalf("single credential = %#v", creds)
	}

	// Add second
	err = s.SetUserCredential(ctx, userID, "alice2", "pass2")
	if err != nil {
		t.Fatal(err)
	}
	creds, err = s.GetUserCredentials(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 2 {
		t.Fatalf("credentials count = %d, want 2", len(creds))
	}
	logins := map[string]bool{}
	for _, c := range creds {
		logins[c.Login] = true
	}
	if !logins["alice"] || !logins["alice2"] {
		t.Fatalf("credentials = %#v", creds)
	}
}

// =============================================================================
// SetUserCredential
// =============================================================================

func TestSetUserCredentialAddNewUpdatePasswordDelete(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "cred-ops", PeerIP: "10.0.3.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"10.0.3.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add new credential
	err = s.SetUserCredential(ctx, userID, "bob", "initial")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AuthenticateUser(ctx, "bob", "initial")
	if err != nil {
		t.Fatalf("initial auth failed: %v", err)
	}

	// Update password
	err = s.SetUserCredential(ctx, userID, "bob", "updated")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AuthenticateUser(ctx, "bob", "updated")
	if err != nil {
		t.Fatalf("updated auth failed: %v", err)
	}
	_, err = s.AuthenticateUser(ctx, "bob", "initial")
	if err == nil {
		t.Fatal("old password should not work after update")
	}

	// Delete credential (empty password)
	err = s.SetUserCredential(ctx, userID, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AuthenticateUser(ctx, "bob", "updated")
	if err == nil {
		t.Fatal("auth should fail after deleting credential")
	}
}

// =============================================================================
// AddUser / UpdateUser with web_auth
// =============================================================================

func TestAddUserWithWebAuthLogin(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "web-login-user", PeerIP: "10.0.4.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "login",
		Networks: []string{"10.0.4.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	user, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.WebAuth != "login" {
		t.Fatalf("web_auth = %q, want login", user.WebAuth)
	}
}

func TestAddUserWithWebAuthBoth(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "web-both-user", PeerIP: "10.0.5.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "both",
		Networks: []string{"10.0.5.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	user, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.WebAuth != "both" {
		t.Fatalf("web_auth = %q, want both", user.WebAuth)
	}
}

func TestUpdateUserWebAuthChanges(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "auth-change", PeerIP: "10.0.6.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "network",
		Networks: []string{"10.0.6.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	user, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	user.WebAuth = "login"
	err = s.UpdateUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}

	user, err = s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.WebAuth != "login" {
		t.Fatalf("web_auth = %q, want login", user.WebAuth)
	}

	// Change to both
	user.WebAuth = "both"
	err = s.UpdateUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}

	user, err = s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.WebAuth != "both" {
		t.Fatalf("web_auth = %q, want both", user.WebAuth)
	}
}

// =============================================================================
// App Settings
// =============================================================================

func TestGetAllSettingsEmpty(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	settings, err := s.GetAllSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// After migration 029, filter_allow and filter_deny are always present (empty).
	// Other keys should not exist in a fresh DB.
	for k := range settings {
		if k != "filter_allow" && k != "filter_deny" {
			t.Fatalf("unexpected setting key %q in fresh DB", k)
		}
	}
}

func TestGetAllSettingsPopulated(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	err := s.SaveSettings(ctx, map[string]string{
		"theme": "dark",
		"lang":  "en",
	})
	if err != nil {
		t.Fatal(err)
	}

	settings, err := s.GetAllSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// filter_allow and filter_deny from migration 029 + our 2 new entries
	if len(settings) != 4 {
		t.Fatalf("settings = %d entries, want 4", len(settings))
	}
	if settings["theme"] != "dark" {
		t.Fatalf("theme = %q, want dark", settings["theme"])
	}
	if settings["lang"] != "en" {
		t.Fatalf("lang = %q, want en", settings["lang"])
	}
}

func TestSaveSettingsInsertAndUpdate(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Insert new
	err := s.SaveSettings(ctx, map[string]string{"key1": "value1"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetAllSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings["key1"] != "value1" {
		t.Fatalf("key1 = %q, want value1", settings["key1"])
	}

	// Update existing
	err = s.SaveSettings(ctx, map[string]string{"key1": "value2", "key2": "value3"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err = s.GetAllSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings["key1"] != "value2" {
		t.Fatalf("key1 = %q after update, want value2", settings["key1"])
	}
	if settings["key2"] != "value3" {
		t.Fatalf("key2 = %q, want value3", settings["key2"])
	}
	// filter_allow and filter_deny from migration 029 + our 2 entries
	if len(settings) != 4 {
		t.Fatalf("settings count = %d, want 4", len(settings))
	}
}

// =============================================================================
// Communities — migration 13 + methods
// =============================================================================

func TestGetCommunities(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Need catalog_entries for communities to be generated
	syncTestData(ctx, t, s)

	// Generate communities for mode 1 since a fresh test store may not have them
	count, err := s.GenerateCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("generated %d communities", count)

	comms, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(comms) == 0 {
		t.Fatal("expected communities to be generated for mode 1")
	}
}

func TestSetCommunityInsertAndUpdate(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	syncTestData(ctx, t, s)

	// Insert a new community for a group
	err := s.SetCommunity(ctx, 1, "TestCat", "", 1000)
	if err != nil {
		t.Fatal(err)
	}

	comms, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if comms["TestCat"] != 1000 {
		t.Fatalf("TestCat community = %d, want 1000", comms["TestCat"])
	}

	// Update same group
	err = s.SetCommunity(ctx, 1, "TestCat", "", 2000)
	if err != nil {
		t.Fatal(err)
	}
	comms, err = s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if comms["TestCat"] != 2000 {
		t.Fatalf("updated TestCat community = %d, want 2000", comms["TestCat"])
	}

	// Insert service-level community
	err = s.SetCommunity(ctx, 1, "TestCat", "TestSvc", 3000)
	if err != nil {
		t.Fatal(err)
	}
	comms, err = s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if comms["TestCat|TestSvc"] != 3000 {
		t.Fatalf("TestCat|TestSvc community = %d, want 3000", comms["TestCat|TestSvc"])
	}
}

func TestSetCommunityRejectsDuplicateNumber(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	syncTestData(ctx, t, s)

	// Set a community on one key
	err := s.SetCommunity(ctx, 1, "CatA", "", 5000)
	if err != nil {
		t.Fatal(err)
	}

	// Try to set same community number on different key
	err = s.SetCommunity(ctx, 1, "CatB", "", 5000)
	if err == nil {
		t.Fatal("expected duplicate community error")
	}
}

func TestGenerateCommunitiesFillsMissing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// First delete all existing communities for mode 1
	_, err := s.DB.Exec("DELETE FROM catalog_communities WHERE mode_id = 1")
	if err != nil {
		t.Fatal(err)
	}

	syncTestData(ctx, t, s)

	// Generate
	count, err := s.GenerateCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("expected > 0 generated communities")
	}

	// Verify communities exist
	comms, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(comms) == 0 {
		t.Fatal("no communities after generate")
	}

	// Running again should not generate more
	count2, err := s.GenerateCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if count2 != 0 {
		t.Fatalf("second GenerateCommunities returned %d, want 0", count2)
	}
}

// TestGenCommunitiesRuntimeRespectsContextCancellation — genCommunitiesRuntime
// used tx.Query/tx.Exec internally, ignoring the ctx callers pass in. A
// canceled/expired request context should abort community generation instead
// of running to completion regardless. Since Store.Transaction's own
// BeginTx(ctx, ...) would already reject an upfront-canceled context before
// ever reaching genCommunitiesRuntime, this calls it directly against a
// separately-started *sql.Tx to isolate what genCommunitiesRuntime itself
// does with the context it's given.
func TestGenCommunitiesRuntimeRespectsContextCancellation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	syncTestData(ctx, t, s)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // test cleanup

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := genCommunitiesRuntime(canceledCtx, tx, 1); err == nil {
		t.Fatal("expected an error from genCommunitiesRuntime with a canceled context, got nil")
	}
}

// =============================================================================
// Prefix counting — additional tests
// =============================================================================

func TestCountSelectionPrefixesEmptySelection(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "empty-sel", PeerIP: "172.16.0.10", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.99.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	v4, v6, err := s.CountSelectionPrefixes(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 0 || v6 != 0 {
		t.Fatalf("empty selection counts: v4=%d v6=%d, want 0/0", v4, v6)
	}
}

func TestCountPrefixesWithExplicitCategoriesAndServices(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Get feed for mode 1
	var feedID int64
	err := s.DB.QueryRow("SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").Scan(&feedID)
	if err != nil {
		t.Fatal(err)
	}

	err = s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "CountA", Service: "Svc1", CIDR: "1.1.1.0/24"},
		{Category: "CountA", Service: "Svc1", CIDR: "2.2.2.0/24"},
		{Category: "CountB", Service: "Svc2", CIDR: "2001:db8::/48"},
	})
	if err != nil {
		t.Fatal(err)
	}

	userID, err := s.AddUser(ctx, User{
		Name: "count-user", PeerIP: "172.16.0.20", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.88.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Clear default route filters — global filters are now in app_settings, empty by default.
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM app_settings WHERE key = 'filter_allow' OR key = 'filter_deny'`); err != nil {
		t.Fatal(err)
	}

	v4, v6, err := s.CountPrefixes(ctx, 1, []string{"CountA"}, nil, userID)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 2 {
		t.Fatalf("CountPrefixes v4 = %d, want 2", v4)
	}
	if v6 != 0 {
		t.Fatalf("CountPrefixes v6 = %d, want 0", v6)
	}

	v4, v6, err = s.CountPrefixes(ctx, 1, nil, []ServiceKey{{Category: "CountB", Service: "Svc2"}}, userID)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 0 {
		t.Fatalf("CountPrefixes v4 = %d, want 0", v4)
	}
	if v6 != 1 {
		t.Fatalf("CountPrefixes v6 = %d, want 1", v6)
	}
}

func TestCountPrefixesEmptyLists(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "empty-count", PeerIP: "172.16.0.30", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.77.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	v4, v6, err := s.CountPrefixes(ctx, 1, nil, nil, userID)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 0 || v6 != 0 {
		t.Fatalf("empty CountPrefixes: v4=%d v6=%d, want 0/0", v4, v6)
	}
}

func TestCategoryPrefixCounts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	var feedID int64
	err := s.DB.QueryRow("SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").Scan(&feedID)
	if err != nil {
		t.Fatal(err)
	}

	err = s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "CatA", Service: "S1", CIDR: "4.4.4.0/24"},
		{Category: "CatA", Service: "S2", CIDR: "5.5.5.0/24"},
		{Category: "CatA", Service: "S1", CIDR: "2001:100::/48"},
		{Category: "CatB", Service: "S3", CIDR: "6.6.6.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	v4, v6, err := s.CategoryPrefixCounts(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	if v4["CatA"] != 2 {
		t.Fatalf("CategoryPrefixCounts v4[CatA] = %d, want 2", v4["CatA"])
	}
	if v6["CatA"] != 1 {
		t.Fatalf("CategoryPrefixCounts v6[CatA] = %d, want 1", v6["CatA"])
	}
	if v4["CatB"] != 1 {
		t.Fatalf("CategoryPrefixCounts v4[CatB] = %d, want 1", v4["CatB"])
	}
}

func TestPrefixCounts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	var feedID int64
	err := s.DB.QueryRow("SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").Scan(&feedID)
	if err != nil {
		t.Fatal(err)
	}

	err = s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "Perf", Service: "A", CIDR: "7.7.7.0/24"},
		{Category: "Perf", Service: "A", CIDR: "8.8.8.0/24"},
		{Category: "Perf", Service: "B", CIDR: "2001:200::/48"},
	})
	if err != nil {
		t.Fatal(err)
	}

	v4, v6, err := s.PrefixCounts(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	if v4["Perf"]["A"] != 2 {
		t.Fatalf("PrefixCounts v4[Perf][A] = %d, want 2", v4["Perf"]["A"])
	}
	if v6["Perf"]["B"] != 1 {
		t.Fatalf("PrefixCounts v6[Perf][B] = %d, want 1", v6["Perf"]["B"])
	}
}

// =============================================================================
// Helpers
// =============================================================================

// syncTestData ensures there are catalog_entries in mode 1 for community tests.
func syncTestData(ctx context.Context, t *testing.T, s *Store) {
	t.Helper()
	var feedID int64
	err := s.DB.QueryRow("SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").Scan(&feedID)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	err = s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feedID).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		err = s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
			{Category: "CommunityTest", Service: "Svc1", CIDR: "99.99.99.0/24"},
			{Category: "CommunityTest", Service: "Svc2", CIDR: "88.88.88.0/24"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// UserByName returns a user by name for testing convenience.
func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, "SELECT id FROM users WHERE name = ?", name).Scan(&id)
	if err != nil {
		return User{}, err
	}
	return s.User(ctx, id)
}

// =============================================================================
// DeleteFeedAdapter
// =============================================================================

func TestDeleteFeedAdapter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Add a custom adapter
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO feed_adapters(name, source) VALUES ('Test Delete', 'function sync(f,a){return[]}')")
	if err != nil {
		t.Fatal(err)
	}

	// Find its ID
	var id int64
	err = s.DB.QueryRowContext(ctx, "SELECT id FROM feed_adapters WHERE name='Test Delete'").Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	// Delete it
	if err := s.DeleteFeedAdapter(ctx, id); err != nil {
		t.Fatal(err)
	}

	// Verify gone
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM feed_adapters WHERE id=?", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("adapter not deleted")
	}
}

func TestDeleteFeedAdapterNotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	err := s.DeleteFeedAdapter(ctx, 99999)
	if !IsNotFound(err) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}
