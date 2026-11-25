#!/bin/bash
# The public FAQ instance (DFC169 §11.4): its own Unix user, its own copy of
# the docs, a hardened user unit on 127.0.0.1:8485. Tailscale Funnel is a
# separate step this script never takes.
#
#   scripts/faq/install-public.sh stage      # as you: build and stage (no sudo)
#   sudo scripts/faq/install-public.sh install STAGE  # as root: user, files, unit, start
#   scripts/faq/install-public.sh status     # is it up, what has it answered
#
# Run `stage` then `install` again to update: the app tree is replaced
# whole, the user's log and caches are kept.
#
# What the ssql-faq user gets: a home at /var/lib/ssql-faq holding its
# caches, its question log and its unit; a root-owned, read-only app tree
# (the faq binary, the slim ssql, docsearch, and the committed user docs:
# no journals, no claude/ notes, no research docs). It cannot read your
# home (mode 750, and the unit makes /home inaccessible anyway).
#
# Its network (DFC171 §5): an nftables rule (ssql-faq-net.service) lets the
# user's processes answer connections and open one to the Ollama filter
# (ssql-faq-ollama-filter.service, a system unit under a dynamic user), and
# nothing else. Code that escapes the jail can ask the filter for a chat
# with the FAQ's model, and cannot pull, delete or replace models, or reach
# the LAN, the tailnet or the llama bridge.
set -euo pipefail

USER_NAME=ssql-faq
HOME_DIR=/var/lib/ssql-faq
APP=$HOME_DIR/app
UNIT=ssql-faq-public.service
SYSTEM_UNITS="ssql-faq-net.service ssql-faq-ollama-filter.service"
LIBEXEC=/usr/local/libexec/ssql-faq
LOG=$HOME_DIR/.local/state/ssql-faq/questions.jsonl
STAGE=${XDG_CACHE_HOME:-$HOME/.cache}/ssql-faq-public-stage

repo() { git -C "$(dirname "$0")" rev-parse --show-toplevel; }

stage() {
	local r
	r=$(repo)
	if [ -n "$(git -C "$r" status --porcelain -- doc README.md CHANGELOG.md cmd/ssql-playground/data scripts/faq scripts/docsearch)" ]; then
		echo "uncommitted changes in the docs or the faq: the public copy is built from HEAD; commit first" >&2
		exit 1
	fi
	rm -rf "$STAGE"
	mkdir -p "$STAGE/app/bin" "$STAGE/app/repo/doc/research"
	# The committed user docs and sample data only.
	git -C "$r" archive HEAD README.md CHANGELOG.md go.mod 'doc/*.md' doc/codelab-data cmd/ssql-playground/data |
		tar -x -C "$STAGE/app/repo"
	# A pathspec's * crosses /, so doc/*.md brought the research docs too.
	find "$STAGE/app/repo/doc" -mindepth 2 -name '*.md' -delete
	mkdir -p "$STAGE/app/repo/doc/research" # docsearch finds its root by go.mod and doc/research
	cp "$r/.docsearch-cache.jsonl" "$STAGE/app/repo/" 2>/dev/null || true
	(cd "$r" && CGO_ENABLED=0 go build -tags slim -ldflags "-s -w" -o "$STAGE/app/bin/ssql" ./cmd/ssql)
	(cd "$r/scripts/faq" && go build -o "$STAGE/app/bin/ssql-faq" .)
	(cd "$r/scripts/docsearch" && go build -o "$STAGE/app/bin/docsearch" .)
	cp "$r/scripts/faq/$UNIT" "$r/scripts/faq/ssql-faq-public.nft" "$STAGE/"
	for u in $SYSTEM_UNITS; do cp "$r/scripts/faq/$u" "$STAGE/"; done
	git -C "$r" rev-parse --short=8 HEAD >"$STAGE/app/REVISION"
	# Embed the copy's chunks now, as you, so the service's first question
	# does not: the service sees the app tree read-only.
	(cd "$STAGE/app/repo" && "$STAGE/app/bin/docsearch" -k 1 warm-up >/dev/null)
	echo "staged $(cat "$STAGE/app/REVISION") in $STAGE; now: sudo $0 install $STAGE"
}

install_root() {
	local src=${1:?usage: sudo $0 install STAGE_DIR}
	[ "$(id -u)" = 0 ] || { echo "install needs root: sudo $0 install $src" >&2; exit 1; }
	[ -x "$src/app/bin/ssql-faq" ] || { echo "$src is not a staged tree; run $0 stage first" >&2; exit 1; }
	if ! getent passwd "$USER_NAME" >/dev/null; then
		useradd --system --user-group --create-home --home-dir "$HOME_DIR" --shell /usr/sbin/nologin "$USER_NAME"
	fi
	local uid
	uid=$(id -u "$USER_NAME")
	# The app tree: root's, readable, never writable by the service.
	rm -rf "$APP.new"
	cp -a "$src/app" "$APP.new"
	chown -R root:root "$APP.new"
	chmod -R u=rwX,go=rX "$APP.new"
	rm -rf "$APP.old"
	[ -d "$APP" ] && mv "$APP" "$APP.old"
	mv "$APP.new" "$APP"
	rm -rf "$APP.old"
	# What the service may write, and its unit.
	install -d -o "$USER_NAME" -g "$USER_NAME" -m 700 "$HOME_DIR/.cache" "$HOME_DIR/.config" "$HOME_DIR/.config/systemd" "$HOME_DIR/.config/systemd/user"
	# The way to the question log is open to the ssql-faq group (the
	# service's -log-shared makes the log itself 0640), and whoever ran sudo
	# joins it: the owner reads the log without sudo, and ssql-faq still
	# reads nothing of theirs.
	install -d -o "$USER_NAME" -g "$USER_NAME" -m 750 "$HOME_DIR/.local" "$HOME_DIR/.local/state"
	if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ] && ! id -nG "$SUDO_USER" | tr ' ' '\n' | grep -qx "$USER_NAME"; then
		usermod -aG "$USER_NAME" "$SUDO_USER"
		echo "added $SUDO_USER to the $USER_NAME group: log in again (or run: newgrp $USER_NAME) to read the log without sudo"
	fi
	install -o "$USER_NAME" -g "$USER_NAME" -m 644 "$src/$UNIT" "$HOME_DIR/.config/systemd/user/$UNIT"
	# Its network: the rule first, then the filter, then the service.
	install -d -m 755 "$LIBEXEC" /etc/ssql-faq
	install -m 755 "$APP/bin/ssql-faq" "$LIBEXEC/ssql-faq"
	sed "s/@UID@/$uid/" "$src/ssql-faq-public.nft" >/etc/ssql-faq/public.nft
	for u in $SYSTEM_UNITS; do install -m 644 "$src/$u" "/etc/systemd/system/$u"; done
	systemctl daemon-reload
	# shellcheck disable=SC2086
	systemctl enable $SYSTEM_UNITS
	# shellcheck disable=SC2086
	systemctl restart $SYSTEM_UNITS
	loginctl enable-linger "$USER_NAME"
	for _ in $(seq 1 20); do [ -S "/run/user/$uid/bus" ] && break; sleep 0.5; done
	systemctl --user -M "$USER_NAME@" daemon-reload
	systemctl --user -M "$USER_NAME@" enable "$UNIT"
	systemctl --user -M "$USER_NAME@" restart "$UNIT"
	check_net
	echo "started $UNIT as $USER_NAME (app $(cat "$APP/REVISION")); check: $0 status"
}

# check_net asks, as the service's user, for what the rule must refuse and
# the filter must pass or refuse, and fails the install on any surprise.
check_net() {
	local as=(runuser -u "$USER_NAME" -- curl -s -o /dev/null --max-time 5 -w '%{http_code}')
	local code
	for _ in $(seq 1 20); do
		code=$("${as[@]}" -X POST -d '{}' http://127.0.0.1:11435/api/pull || true)
		[ "$code" = 403 ] && break
		sleep 0.5
	done
	[ "$code" = 403 ] || { echo "FAIL: the Ollama filter did not refuse /api/pull for $USER_NAME (got '$code')" >&2; exit 1; }
	for target in http://127.0.0.1:11434/api/tags http://[::1]:11434/api/tags http://127.0.0.1:8765/; do
		if code=$("${as[@]}" "$target"); then
			echo "FAIL: $USER_NAME reaches $target ($code): the network rule is not in force" >&2
			exit 1
		fi
	done
	echo "network: $USER_NAME reaches the Ollama filter only (Ollama itself and the llama bridge refused)"
}

status() {
	systemctl --user -M "$USER_NAME@" status "$UNIT" --no-pager -n 5 2>/dev/null ||
		sudo systemctl --user -M "$USER_NAME@" status "$UNIT" --no-pager -n 5 || true
	echo "network rule and Ollama filter: $(systemctl is-active $SYSTEM_UNITS | tr '\n' ' ')"
	echo "health (today's answered and refused questions):"
	curl -s --max-time 5 http://127.0.0.1:8485/api/health && echo
	echo "today's answers by status ($LOG):"
	# Read the log (as a member of the group, else through sudo), then
	# summarise it. Until 2026-11-26 the readable case read it and printed
	# nothing: the summary sat in the sudo branch's else.
	local lines ok=1
	if [ -r "$LOG" ]; then
		lines=$(cat "$LOG")
	elif ! lines=$(sudo -u "$USER_NAME" sh -c "test ! -e '$LOG' || cat '$LOG'"); then
		ok=0
		echo "  could not read the log (not in the $USER_NAME group yet, and sudo refused)"
	fi
	if [ "$ok" = 1 ] && [ -z "$lines" ]; then
		echo "  nothing asked yet"
	elif [ "$ok" = 1 ]; then
		printf '%s\n' "$lines" |
			ssql from jsonl | ssql where -if time ge "$(date -u +%F)" | ssql group-by status -count n | ssql to table || true
	fi
	echo "Funnel (on: tailscale funnel --bg --https=8443 8485; off: tailscale funnel --https=8443 off):"
	tailscale funnel status 2>&1 | sed 's/^/  /'
}

case ${1:-} in
stage) stage ;;
install) install_root "${2:-}" ;;
status) status ;;
*)
	sed -n '2,13p' "$0"
	exit 2
	;;
esac
