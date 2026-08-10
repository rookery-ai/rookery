#!/usr/bin/env bash
# Install each Linux artifact and run it.
#
# This is the gate that was missing when the deb, rpm and every archive shipped
# without their SQL migrations: nothing in CI had ever installed a package and
# started it, so all three failed on first use for the whole history of the repo.
#
# Every install happens inside a throwaway container, so no sudo is needed and
# the host is untouched — which also lets the rpm case run on a Debian CI runner
# and the deb case run on a Fedora workstation.
#
# Usage: scripts/smoke-package.sh [dist-dir]        (default: dist)
set -euo pipefail

DIST="${1:-dist}"
ENGINE="${CONTAINER_ENGINE:-$(command -v podman >/dev/null 2>&1 && echo podman || echo docker)}"

fail() { echo "::error::$*" >&2; exit 1; }

pick_artifact() {
	# Exactly one artifact must match, otherwise a stale dist/ would silently
	# smoke-test the previous build.
	#
	# .goreleaser.yaml sets no nfpm file_name_template, so goreleaser's default
	# applies and an rpm is named rookery_<ver>_linux_amd64.rpm — NOT the
	# rpm-conventional rookery-<ver>.x86_64.rpm. Both spellings are accepted so
	# that setting a template later cannot silently break this gate.
	#
	# Excluding darwin/windows by name (rather than requiring the literal string
	# "linux") is what actually discriminates the OS here: the archive formats
	# (tar.gz) are also built for darwin, and "amd64" alone matches
	# rookery_<ver>_darwin_amd64.tar.gz just as well as the linux one, which
	# would make this "exactly 1" check pass while silently picking the wrong
	# OS's archive. Requiring "*linux*" would also wrongly reject the
	# rpm-conventional spelling above, which contains no "linux" substring —
	# excluding the other two OSes is the only condition that holds for both
	# accepted rpm spellings.
	local ext="$1"
	local -a hits
	mapfile -t hits < <(find "$DIST" -maxdepth 1 -name "*.$ext" \
		\( -name '*amd64*' -o -name '*x86_64*' \) \
		! -name '*darwin*' ! -name '*windows*' | sort)
	[ "${#hits[@]}" -eq 1 ] \
		|| fail "expected exactly 1 linux amd64 .$ext in $DIST, found ${#hits[@]}: ${hits[*]:-none}"
	printf '%s\n' "${hits[0]}"
}

DEB="$(pick_artifact deb)"
RPM="$(pick_artifact rpm)"
TGZ="$(pick_artifact tar.gz)"

# Run inside the container: bootstrap from / (a systemd user unit has no
# WorkingDirectory, so its CWD is $HOME — never the source tree), then serve and
# probe. `rookery healthcheck` is used rather than curl because a minimal base
# image is not guaranteed to ship one.
readonly RUN_SCRIPT='
set -euo pipefail
cd /
rookery version
rookery owner bootstrap -u smoke -p "smoke-pw-12345"
rookery serve >/tmp/serve.log 2>&1 &
for i in $(seq 1 45); do
	if rookery healthcheck >/dev/null 2>&1; then
		echo "OK: healthy"
		exit 0
	fi
	sleep 1
done
echo "server never became healthy" >&2
cat /tmp/serve.log >&2
exit 1
'

smoke_in_container() {
	local label="$1" image="$2" artifact="$3" install_cmd="$4"
	echo "==> $label"
	"$ENGINE" run --rm \
		-v "$(realpath "$artifact")":/artifact:ro,Z \
		-e ROOKERY_DATA_DIR=/tmp/rookery-data \
		"$image" \
		bash -c "set -euo pipefail; $install_cmd; $RUN_SCRIPT" \
		|| fail "$label failed"
	echo "==> $label OK"
}

smoke_in_container "rpm on fedora" "fedora:latest" "$RPM" \
	'rpm -i /artifact'

smoke_in_container "deb on debian" "debian:stable-slim" "$DEB" \
	'dpkg -i /artifact'

# The archive runs on the host, extracted to one directory and executed from a
# completely different one. That is the case the deleted exe-relative probe used
# to accidentally paper over whenever someone ran from the source tree.
echo "==> tar.gz from an unrelated CWD"
extract_dir="$(mktemp -d)"
run_dir="$(mktemp -d)"
data_dir="$(mktemp -d)"
serve_pid=""
cleanup_tgz() {
	# Kill the backgrounded server on every exit path (success, failure, or
	# early `fail()`) so the run leaves nothing behind.
	[ -n "$serve_pid" ] && kill "$serve_pid" 2>/dev/null || true
	rm -rf "$extract_dir" "$run_dir" "$data_dir"
}
trap cleanup_tgz EXIT

tar -xzf "$TGZ" -C "$extract_dir"
[ -x "$extract_dir/rookery" ] || fail "archive has no executable rookery at its root"

(
	cd "$run_dir"
	export ROOKERY_DATA_DIR="$data_dir" ROOKERY_PORT=18099
	"$extract_dir/rookery" owner bootstrap -u smoke -p 'smoke-pw-12345'
) || fail "tar.gz bootstrap failed from an unrelated CWD"

# `serve` is started in THIS shell (not a subshell) so `$!` is the real PID and
# the process is a direct child the trap can kill outright, instead of relying
# on a background job surviving a subshell's exit/reparenting.
cd "$run_dir"
export ROOKERY_DATA_DIR="$data_dir" ROOKERY_PORT=18099
"$extract_dir/rookery" serve >"$run_dir/serve.log" 2>&1 &
serve_pid=$!

# `rookery healthcheck` resolves its target port the same way `serve` does
# (config.Load reads ROOKERY_PORT from the environment), so probing with the
# same env var — rather than hardcoding 8080 or reaching for curl, which a
# minimal host may lack — reuses the exact mechanism the container cases use.
healthy=0
for i in $(seq 1 45); do
	if ROOKERY_PORT=18099 "$extract_dir/rookery" healthcheck >/dev/null 2>&1; then
		healthy=1
		break
	fi
	sleep 1
done
if [ "$healthy" -ne 1 ]; then
	echo "tar.gz server never became healthy" >&2
	cat "$run_dir/serve.log" >&2
	fail "tar.gz serve failed from an unrelated CWD"
fi
echo "==> tar.gz OK"

echo "all package smoke tests passed"
