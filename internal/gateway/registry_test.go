package gateway

import (
	"context"
	"testing"
)

type fakeGW struct{ platform string }

func (f *fakeGW) Platform() string                       { return f.platform }
func (f *fakeGW) OwnerUserID() string                    { return "ws1" }
func (f *fakeGW) Start(ctx context.Context) error        { <-ctx.Done(); return nil }
func (f *fakeGW) Stop() error                            { return nil }
func (f *fakeGW) Send(platformUserID, text string) error { return nil }

func TestRegisterAndLookupAdapter(t *testing.T) {
	RegisterAdapter("fake", func(token, config, ws string, d DispatchFunc) (Gateway, error) {
		return &fakeGW{platform: "fake"}, nil
	})
	f, ok := adapterFactory("fake")
	if !ok {
		t.Fatal("factory not registered")
	}
	gw, err := f("t", "", "ws1", func(context.Context, Message) {})
	if err != nil || gw.Platform() != "fake" {
		t.Fatalf("factory produced wrong gateway: %v %v", gw, err)
	}
}

func TestUnknownAdapterNotFound(t *testing.T) {
	if _, ok := adapterFactory("does-not-exist"); ok {
		t.Fatal("expected unknown adapter to be absent")
	}
}
