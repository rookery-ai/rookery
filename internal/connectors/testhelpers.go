package connectors

// SetActionsForTest replaces a provider's actions in-place. It exists so tests in
// OTHER packages (e.g. internal/coder's dispatch test) can rewrite an action's request
// URL to point at an httptest server. Not for production use.
func (r *Registry) SetActionsForTest(provider string, actions []Action) {
	r.actions[provider] = actions
}
