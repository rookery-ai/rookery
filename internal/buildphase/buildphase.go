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

	// KindEnvVar narrows a build to the thing being built. Agent builds and skill builds
	// share Generation, but only a SKILL build is forbidden to install packages: a skill
	// is written for an agent to install at run time, while an agent build runs real
	// end-to-end tests. Read from the coder's env on both doors (API engine and the
	// `rookery deps` subcommand), so there is one source of truth.
	KindEnvVar = "ROOKERY_BUILD_KIND"
	// KindSkill is KindEnvVar's value during a skill build.
	KindSkill = "skill"
)
