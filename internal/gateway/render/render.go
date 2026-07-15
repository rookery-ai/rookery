// Package render converts neutral CommonMark (emitted by the gateway router)
// into each chat platform's native markup. The router is platform-agnostic;
// each adapter renders on the way out.
package render

import "sync"

// Renderer converts neutral CommonMark into a platform's markup.
type Renderer interface {
	Render(commonMark string) string
}

// RendererFunc adapts an ordinary function to the Renderer interface.
type RendererFunc func(string) string

// Render implements Renderer.
func (f RendererFunc) Render(s string) string { return f(s) }

// passthrough returns its input unchanged (CommonMark-native platforms).
var passthrough = RendererFunc(func(s string) string { return s })

// Passthrough returns a Renderer that emits CommonMark unchanged.
func Passthrough() Renderer { return passthrough }

var (
	mu       sync.RWMutex
	registry = map[string]Renderer{}
)

// Register associates a renderer with a platform name. Call from init().
func Register(platform string, r Renderer) {
	mu.Lock()
	defer mu.Unlock()
	registry[platform] = r
}

// For returns the renderer registered for platform, or Passthrough if none.
func For(platform string) Renderer {
	mu.RLock()
	defer mu.RUnlock()
	if r, ok := registry[platform]; ok {
		return r
	}
	return passthrough
}
