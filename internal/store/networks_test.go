package store

import (
	"reflect"
	"testing"
)

func TestAggregateNetworksMasksAndSorts(t *testing.T) {
	got, err := AggregateNetworks([]string{"10.0.0.5/24", "10.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/24", "10.0.2.0/24"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateNetworksMergesAdjacent(t *testing.T) {
	got, err := AggregateNetworks([]string{"10.0.0.0/25", "10.0.0.128/25"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/24"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateNetworksIgnoresInputOrderAndSpacing(t *testing.T) {
	got, err := AggregateNetworks([]string{"  10.0.2.0/24 ", "10.0.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/24", "10.0.2.0/24"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateNetworksRejectsInvalidCIDR(t *testing.T) {
	_, err := AggregateNetworks([]string{"10.0.0.0/24", "not-a-cidr"})
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestAggregateNetworksEmptyInput(t *testing.T) {
	got, err := AggregateNetworks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
