package tools

import (
	"context"
	"testing"
)

func TestToolsCanReportPartialOutput(t *testing.T) {
	Update(context.Background(), "nobody listens") // must not panic
	var got []string
	ctx := WithUpdates(context.Background(), func(p string) { got = append(got, p) })
	Update(ctx, "one")
	Update(ctx, "two")
	if len(got) != 2 || got[1] != "two" || Updates(ctx) == nil || Updates(context.Background()) != nil {
		t.Errorf("updates: %v", got)
	}
}
