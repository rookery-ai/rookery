// Package buildphase defines the marker that tells the coder engine a run is an
// agent/skill BUILD (generation/verification), not a real scheduled/manual run. It lives
// in its own tiny package because the self-managed-OAuth connector build-guard reads it.
package buildphase

const (
	// EnvVar is set in the coder subprocess/engine environment during a build.
	EnvVar = "ROOKERY_BUILD_PHASE"
	// Generation is EnvVar's value during agent/skill generation + edit verification. At
	// build time the connector Execute guard refuses mutating actions (real sends/deletes).
	Generation = "generation"
)
