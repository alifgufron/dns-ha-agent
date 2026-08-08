#!/bin/sh
#
# Install dns-ha-agent on FreeBSD.
#
# Build requirements: Go (version per go.mod) and nothing else — CGO is off,
# there are no third-party modules and no C compiler is needed. Installing onto
# a live system additionally requires FreeBSD and root.
#
# Build only (e.g. from a Linux dev box, cross-compiling for FreeBSD):
#   BUILD_ONLY=1 GOOS=freebsd GOARCH=amd64 sh scripts/install.sh
#
# Stage into a packaging root instead of the live system:
#   DESTDIR=/tmp/pkg sh scripts/install.sh

set -e

BINDIR="${DESTDIR}/usr/local/bin"
CONFDIR="${DESTDIR}/usr/local/etc"
RCDIR="${DESTDIR}/usr/local/etc/rc.d"
LOGDIR="${DESTDIR}/var/log"
DBDIR="${DESTDIR}/var/db"

HOST_OS=$(uname -s)

# --- Target platform (respect GOOS/GOARCH overrides) ---
GOOS="${GOOS:-$(echo "${HOST_OS}" | tr '[:upper:]' '[:lower:]')}"
ARCH=$(uname -m)
case "${ARCH}" in
	x86_64|amd64)        GOARCH="${GOARCH:-amd64}" ;;
	aarch64|arm64)       GOARCH="${GOARCH:-arm64}" ;;
	i386|i486|i586|i686) GOARCH="${GOARCH:-386}" ;;
	armv7*)              GOARCH="${GOARCH:-arm}" ;;
	*)                   GOARCH="${GOARCH:-${ARCH}}" ;;
esac

# --- Build dependencies ---
# Only the Go toolchain is needed: no cgo, no C compiler, no external Go
# modules (stdlib only). Everything else is provided by the base system.
GO_REQUIRED=$(awk '/^go /{print $2; exit}' go.mod)

if ! command -v go >/dev/null 2>&1; then
	echo "error: go toolchain not found — Go ${GO_REQUIRED}+ is required to build." >&2
	echo "  FreeBSD:      pkg install go" >&2
	echo "  Debian/Ubuntu: apt install golang-go   (or https://go.dev/dl/)" >&2
	echo "  RHEL/Rocky:   dnf install golang" >&2
	echo "" >&2
	echo "Already have a binary? Skip this script and install manually —" >&2
	echo "see docs/usage.md -> 'Install without a Go toolchain'." >&2
	exit 1
fi

# Compare version as major.minor so e.g. go1.23 is rejected against go 1.24.0.
GO_VERSION=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
if [ -n "${GO_VERSION}" ] && [ -n "${GO_REQUIRED}" ]; then
	if ! awk -v have="${GO_VERSION}" -v want="${GO_REQUIRED}" 'BEGIN {
		split(have, h, "."); split(want, w, ".");
		exit !((h[1] > w[1]) || (h[1] == w[1] && h[2] >= w[2]));
	}'; then
		echo "error: Go ${GO_VERSION} is too old — this project requires ${GO_REQUIRED}+." >&2
		echo "       Upgrade the toolchain (FreeBSD: pkg upgrade go) or fetch a newer" >&2
		echo "       release from https://go.dev/dl/" >&2
		exit 1
	fi
fi
echo "Go toolchain: ${GO_VERSION:-unknown} (requires ${GO_REQUIRED}+)"

# --- Build ---
echo "Building dns-ha-agent for ${GOOS}/${GOARCH}..."
BINNAME="dns-ha-agent-${GOOS}-${GOARCH}"
mkdir -p build
GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED=0 \
	go build -trimpath -o "build/${BINNAME}" ./cmd/dns-ha-agent
echo "  => build/${BINNAME}"

if [ -n "${BUILD_ONLY}" ]; then
	echo "BUILD_ONLY set — skipping install."
	exit 0
fi

# --- Install guards ---
# Staging into DESTDIR is fine anywhere (packaging). Installing onto the live
# system is only valid on FreeBSD: the agent drives CARP via ifconfig/sysctl
# and ships an rc.d script.
if [ -z "${DESTDIR}" ]; then
	if [ "${HOST_OS}" != "FreeBSD" ]; then
		echo "error: this host is ${HOST_OS}, but dns-ha-agent only runs on FreeBSD." >&2
		echo "       The binary was built above; copy it to the FreeBSD node, or re-run" >&2
		echo "       with BUILD_ONLY=1 (build only) or DESTDIR=... (stage only)." >&2
		exit 1
	fi
	if [ "${GOOS}" != "freebsd" ]; then
		echo "error: refusing to install a ${GOOS} binary on FreeBSD." >&2
		exit 1
	fi
	if [ "$(id -u)" -ne 0 ]; then
		echo "error: must be root to install (or set DESTDIR to stage elsewhere)." >&2
		exit 1
	fi
fi

# --- Install ---
echo "Installing dns-ha-agent..."
mkdir -p "${BINDIR}" "${CONFDIR}" "${RCDIR}" "${LOGDIR}" "${DBDIR}"

install -m 0555 "build/${BINNAME}" "${BINDIR}/dns-ha-agent"

if [ ! -f "${CONFDIR}/dns-ha-agent.yaml" ]; then
	install -m 0640 configs/config.yaml "${CONFDIR}/dns-ha-agent.yaml"
	echo "  => Installed default config to ${CONFDIR}/dns-ha-agent.yaml"
	echo "  => EDIT this file and set your token, password, and peers"
else
	echo "  => Config already exists at ${CONFDIR}/dns-ha-agent.yaml, skipping"
fi

install -m 0555 scripts/rc.d/dns-ha-agent "${RCDIR}/dns-ha-agent"

# Validate the installed config early so a bad file is caught before boot.
if [ -z "${DESTDIR}" ]; then
	echo "Validating config..."
	"${BINDIR}/dns-ha-agent" -t -config "${CONFDIR}/dns-ha-agent.yaml" || \
		echo "  => Config needs editing before the service will start."
fi

echo ""
echo "Installation complete."
echo ""
echo "Next steps:"
echo "  1. Edit ${CONFDIR}/dns-ha-agent.yaml"
echo "     - health.process_names: [\"dnsdist\"] or [\"named\"] (BIND9) or both"
echo "     - peer.bind must be THIS node's management IP"
echo "  2. Set secrets in /etc/rc.conf.d/dns-ha-agent:"
echo "       mkdir -p /etc/rc.conf.d"
echo "       cat > /etc/rc.conf.d/dns-ha-agent << 'EOF'"
echo "       export HA_TOKEN=\"YOUR_SHARED_SECRET\""
echo "       export SMTP_PASS=\"your-smtp-password\""
echo "       EOF"
echo "       chmod 0600 /etc/rc.conf.d/dns-ha-agent"
echo "  3. Enable the service:"
echo "       sysrc dns_ha_agent_enable=YES"
echo "  4. Start the service:"
echo "       service dns-ha-agent start"
echo "  5. Check status:"
echo "       service dns-ha-agent status"
echo ""
echo "See docs/usage.md for detailed documentation."
