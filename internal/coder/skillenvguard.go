package coder

import "regexp"

// skillEnvExecRe matches a command that EXECUTES a skill environment — its interpreter,
// pip, or activate script — or points Python at its installed packages. Reading files an
// environment holds (a report a script left there) does not match.
var skillEnvExecRe = regexp.MustCompile(`\.local/share/rookery/envs/[^/\s"']+/(bin/(python[0-9.]*|pip[0-9.]*|activate)\b|lib(64)?/python[0-9.]+/)`)

// invokesSkillEnv reports whether a bash command runs code from a skill environment.
//
// The 2026-10-27 trading run did exactly this for two of three tickers, after run_in_env
// put its first note in the wrong folder: the desk then ran with every workspace secret,
// the run's bridge tokens and the whole workspace home (coder login, every other skill's
// environment) instead of run_in_env's one named key. This closes the route a model takes
// by default; it is pattern matching on the command text, so a command that cd's into an
// environment first, or builds the path in a variable, gets past it. Moving environments
// out of the agent sandbox is the airtight version.
func invokesSkillEnv(command string) bool {
	return skillEnvExecRe.MatchString(command)
}

const skillEnvRefusal = "error: this command runs code from a skill environment, which bash does not do: " +
	"run skill scripts with run_in_env (env, script, args, secrets) so they get only the secrets " +
	"they name. Reading files inside an environment is fine."
