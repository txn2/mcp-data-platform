#!/usr/bin/env bash
# install-test-browser.sh -- install the Chromium the portal's Playwright
# suites run, in CI (#2609).
#
# The browser comes from Playwright's download host. Its system libraries and
# fonts come from apt, as `playwright install --with-deps` gets them, so a run
# draws text with the fonts it always has. apt is the step that cannot be
# relied on: on 2026-11-11 every acceptance job of the E2E Nightly failed in
# `--with-deps` because the runner could not resolve the Ubuntu archives for
# ten minutes, before any test ran. So each apt attempt is bounded, it is tried
# again, and when it still fails the browser is started on the libraries the
# runner image already has (GitHub's Ubuntu images ship Google Chrome, which
# needs the same ones). If it starts, the run goes on with a warning that the
# fonts may differ. Only a browser that cannot start fails the step.
#
# Environment: BROWSER_INSTALL_ATTEMPTS (default 3), BROWSER_DEPS_ATTEMPTS
# (default 2), BROWSER_DEPS_TIMEOUT seconds per apt attempt (default 240),
# BROWSER_RETRY_PAUSE seconds before the first retry, doubled after each
# (default 15).
set -euo pipefail

cd "$(dirname "$0")/../ui"

install_attempts=${BROWSER_INSTALL_ATTEMPTS:-3}
deps_attempts=${BROWSER_DEPS_ATTEMPTS:-2}
deps_timeout=${BROWSER_DEPS_TIMEOUT:-240}
pause=${BROWSER_RETRY_PAUSE:-15}

# retry <attempts> <command...> runs a command until it succeeds, pausing
# between attempts, and fails when the last attempt does.
retry() {
	local attempts=$1 wait=$pause i
	shift
	for ((i = 1; i <= attempts; i++)); do
		if "$@"; then
			return 0
		fi
		if ((i < attempts)); then
			echo "install-test-browser: attempt $i of $attempts failed: $*; trying again in ${wait}s" >&2
			sleep "$wait"
			wait=$((wait * 2))
		fi
	done
	echo "install-test-browser: $attempts attempts failed: $*" >&2
	return 1
}

# launches reports whether the installed Chromium starts and stops.
launches() {
	node -e '
		require("@playwright/test").chromium.launch()
			.then((b) => b.close())
			.catch((e) => { console.error(String(e.message).split("\n")[0]); process.exit(1); });
	'
}

retry "$install_attempts" npx playwright install chromium

if [[ "$(uname -s)" != Linux ]]; then
	exit 0
fi

# timeout signals the whole process group, so the apt-get Playwright starts
# under sudo stops with it and leaves no lock for the next attempt.
if retry "$deps_attempts" timeout --kill-after=15 "$deps_timeout" npx playwright install-deps chromium; then
	exit 0
fi

if launches; then
	msg="apt could not install Chromium's system packages; the browser starts on the runner image's own libraries, so its fonts may differ from a normal run"
	echo "::warning title=Test browser::$msg"
	echo "install-test-browser: $msg" >&2
	exit 0
fi
echo "install-test-browser: apt failed and Chromium does not start without its system packages" >&2
exit 1
