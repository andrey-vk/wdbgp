// +build ignore

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func main() {
	s, err := store.Open(filepath.Join("/tmp", "debug-test.sqlite3"))
	if err != nil {
		panic(err)
	}
	defer s.Close()
	
	ctx := context.Background()
	_, err = s.AddUser(ctx, store.User{
		Name: "user-a", PeerIP: "10.0.0.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"10.0.0.0/24"},
	})
	fmt.Printf("user-a err: %v\n", err)
	
	_, err = s.AddUser(ctx, store.User{
		Name: "user-b", PeerIP: "10.0.0.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"10.0.0.0/24"},
	})
	fmt.Printf("user-b err: %v\n", err)
}
