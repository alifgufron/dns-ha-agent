#!/bin/sh
#
# Uninstall dns-ha-agent on FreeBSD.
#
# Stops the service, removes the binary, rc.d script, and optionally (with
# PURGE=1) the config, log, and state files.
#
# Safe by default: config, log, and state data are KEPT unless PURGE=1 is set.
# The state file only matters while the agent runs (avoids stale-transition
# emails after a restart); the log and config may hold data you want to keep.
#
#   sh scripts/uninstall.sh          # stop + remove binary + rc.d script
#   PURGE=1 sh scripts/uninstall.sh  # also delete config, log, state files
#
# Staging into a packaging root instead of the live system:
#   DESTDIR=/tmp/pkg sh scripts/uninstall.sh

set -e

BINDIR="${DESTDIR}/usr/local/bin"
CONFDIR="${DESTDIR}/usr/local/etc"
RCDIR="${DESTDIR}/usr/local/etc/rc.d"
LOGDIR="${DESTDIR}/var/log"
DBDIR="${DESTDIR}/var/db"

# The paths below follow what install.sh / the default config write. If the
# deployed config used custom paths, adapt these (or pass PURGE=1 after editing).
LOGFILE="${DESTDIR}/var/log/dns-ha-agent.log"
STATEFILE="${DESTDIR}/var/db/dns-ha-agent.state"
CONFFILE="${CONFDIR}/dns-ha-agent.yaml"
BIN="${BINDIR}/dns-ha-agent"
RCSCRIPT="${RCDIR}/dns-ha-agent"

if [ -z "${DESTDIR}" ]; then
	if [ "$(id -u)" -ne 0 ]; then
		echo "error: must be root to uninstall (or set DESTDIR to stage elsewhere)." >&2
		exit 1
	fi

	# Stop the service first so a running agent does not recreate files.
	if [ -x "${RCSCRIPT}" ] && service dns-ha-agent status >/dev/null 2>&1; then
		echo "Stopping dns-ha-agent..."
		service dns-ha-agent stop >/dev/null 2>&1 || true
	fi
	sysrc -x dns_ha_agent_enable 2>/dev/null || true
fi

echo "Removing binary:        ${BIN}"
rm -f "${BIN}"

echo "Removing rc.d script:   ${RCSCRIPT}"
rm -f "${RCSCRIPT}"

if [ -n "${PURGE}" ]; then
	echo "PURGE=1 — removing config, log, and state files."
	echo "Removing config:       ${CONFFILE}"
	rm -f "${CONFFILE}"
	echo "Removing log file:     ${LOGFILE}"
	rm -f "${LOGFILE}"
	echo "Removing state file:   ${STATEFILE}"
	rm -f "${STATEFILE}"

	# Drop now-empty dirs if nothing else uses them (best effort, never fails).
	rmdir "${LOGDIR}" "${DBDIR}" "${RCDIR}" "${CONFDIR}" "${BINDIR}" 2>/dev/null || true
else
	echo ""
	echo "Kept (set PURGE=1 to remove):"
	echo "  config:   ${CONFFILE}"
	echo "  log file: ${LOGFILE}"
	echo "  state:    ${STATEFILE}"
fi

echo ""
echo "Uninstall complete."
echo "Secrets in /etc/rc.conf.d/dns-ha-agent were left untouched — remove them" 
echo "manually if desired (the file only takes effect while the service is enabled)."
