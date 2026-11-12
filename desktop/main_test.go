package main

import (
	"reflect"
	"slices"
	"testing"
)

// The page may call exactly these methods. Wails exposes every exported method of a service, so anything else on Service (or a method with a
// channel or function parameter, like bridge.Backend.Attach) would leak into the page and the binding generator.
func TestServiceExposesOnlyThePageAPI(t *testing.T) {
	var got []string
	typ := reflect.TypeOf(&Service{})
	for i := range typ.NumMethod() {
		got = append(got, typ.Method(i).Name)
	}
	slices.Sort(got)
	// ServiceStartup and ServiceShutdown are the lifecycle hooks Wails calls itself and never binds.
	want := []string{"Answer", "Cancel", "Dismiss", "Info", "Send", "ServiceShutdown", "ServiceStartup"}
	if !slices.Equal(got, want) {
		t.Fatalf("exported methods = %v, want %v", got, want)
	}
}
