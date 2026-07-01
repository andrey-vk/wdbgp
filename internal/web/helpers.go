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
