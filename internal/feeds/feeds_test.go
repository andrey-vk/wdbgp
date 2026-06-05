package feeds

import "testing"

func TestParseCanonicalNormalizesAndDeduplicates(t *testing.T) {
	entries, err := Parse([]byte(`{"entries":[
		{"category":"Messengers","service":"Telegram","cidrs":["149.154.167.99/20","149.154.160.0/20"]}
	]}`), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].CIDR != "149.154.160.0/20" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestParseOpenCCKFlatUsesGroupLookup(t *testing.T) {
	entries, err := Parse([]byte(`{"Telegram":["149.154.167.99/20"]}`),
		map[string][]string{"Telegram": {"Messengers"}}, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Category != "Messengers" ||
		entries[0].Service != "Telegram" || entries[0].CIDR != "149.154.160.0/20" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestMetadataURL(t *testing.T) {
	got := MetadataURL("https://iplist.opencck.org/?format=json&data=cidr4")
	want := "https://iplist.opencck.org/?data=group&format=json"
	if got != want {
		t.Fatalf("MetadataURL = %q, want %q", got, want)
	}
}
