#!/usr/bin/env bash

set -euo pipefail

target="${SRL_TARGET:-}"
username="${SRL_USERNAME:-admin}"
password="${SRL_PASSWORD:-NokiaSrl1!}"
filter_name="${ACL_FILTER_NAME:-cpm}"
sequence_id="${ACL_SEQUENCE_ID:-680}"
exporter_port="${NODE_EXPORTER_PORT:-9100}"
image="${GNMIC_IMAGE:-ghcr.io/openconfig/gnmic:latest}"

usage() {
    cat <<'EOF'
Usage: setup-cpm-acl.sh --target HOST:PORT [options]

Permit inbound node-exporter scrapes through the control-plane filter, then
bind that filter to system control-plane-traffic input. One gNMI commit updates
IPv4 and IPv6:

  - TCP destination port of node-exporter
  - bind acl-filter <name> type ipv4 and ipv6 under input

Safe to run again: the same sequence IDs are replaced.

Options:
  -a, --target HOST:PORT       SR Linux gNMI address (required)
  -u, --username USER          gNMI username (default: admin)
  -p, --password PASSWORD      gNMI password (default: NokiaSrl1!)
      --filter-name NAME       IPv4/IPv6 ACL filter name (default: cpm)
      --sequence-id ID         Exporter ACL entry (default: 680)
      --port PORT              Inbound node-exporter port (default: 9100)
      --image IMAGE            gnmic container image
                              (default: ghcr.io/openconfig/gnmic:latest)
  -h, --help                   Show this help

The same values can be set with SRL_TARGET, SRL_USERNAME, SRL_PASSWORD,
ACL_FILTER_NAME, ACL_SEQUENCE_ID, NODE_EXPORTER_PORT, and GNMIC_IMAGE.
EOF
}

while (($#)); do
    case "$1" in
        -a|--target)
            target="${2:?missing target}"
            shift 2
            ;;
        -u|--username)
            username="${2:?missing username}"
            shift 2
            ;;
        -p|--password)
            password="${2:?missing password}"
            shift 2
            ;;
        --filter-name)
            filter_name="${2:?missing filter name}"
            shift 2
            ;;
        --sequence-id)
            sequence_id="${2:?missing sequence ID}"
            shift 2
            ;;
        --port)
            exporter_port="${2:?missing exporter port}"
            shift 2
            ;;
        --image)
            image="${2:?missing image}"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ -z "$target" ]]; then
    echo "--target or SRL_TARGET is required" >&2
    exit 2
fi

valid_port() {
    [[ "$1" =~ ^[0-9]+$ ]] && (( $1 >= 1 && $1 <= 65535 ))
}

if ! valid_port "$exporter_port"; then
    echo "exporter port must be between 1 and 65535" >&2
    exit 2
fi
if [[ ! "$sequence_id" =~ ^[0-9]+$ ]] || ((sequence_id > 65535)); then
    echo "sequence ID must be between 0 and 65535" >&2
    exit 2
fi

acl="/acl/acl-filter[name=${filter_name}]"
binding="/system/control-plane-traffic/input/acl/acl-filter[name=${filter_name}]"

tcp_entry() {
    local family="$1" protocol_field="$2" protocol_value="$3" port_field="$4" port="$5" description="$6"
    printf '{"description":"%s","match":{"%s":{"%s":"%s"},"transport":{"%s":{"value":%s}}},"action":{"accept":{}}}' \
        "$description" "$family" "$protocol_field" "$protocol_value" "$port_field" "$port"
}

docker run --rm --network host "$image" \
    --address "$target" \
    --username "$username" \
    --password "$password" \
    --skip-verify \
    --encoding json_ietf \
    --timeout 2m \
    set \
    --update-path "${acl}[type=ipv4]/entry[sequence-id=${sequence_id}]" \
    --update-value "$(tcp_entry ipv4 protocol tcp destination-port "$exporter_port" "Accept TCP to node-exporter")" \
    --update-path "${acl}[type=ipv6]/entry[sequence-id=${sequence_id}]" \
    --update-value "$(tcp_entry ipv6 next-header tcp destination-port "$exporter_port" "Accept TCP to node-exporter")" \
    --update-path "${binding}[type=ipv4]" \
    --update-value '{}' \
    --update-path "${binding}[type=ipv6]" \
    --update-value '{}'
