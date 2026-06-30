package web

import (
	"github.com/andrey-vk/wdbgp/internal/store"
)

func countEnabledFeeds(feeds []store.Feed) int {
	count := 0
	for _, feed := range feeds {
		if feed.Enabled {
			count++
		}
	}
	return count
}

func fieldByKey(key string) *settingField {
	for _, f := range allSettings() {
		if f.Key == key {
			return &f
		}
	}
	return nil
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
