#!/usr/bin/env bash
# Apply a Sub2API Docker image archive produced by `make docker-export`.

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

usage() {
    cat <<'EOF'
Usage:
  ./recreate.sh <sub2api-image.tgz> [compose-file]

Apply a Docker image archive produced by `make docker-export`, then recreate the
Sub2API application container with Docker Compose.

Examples:
  ./recreate.sh sub2api-a1b2c3d.tgz
  ./recreate.sh ../sub2api-a1b2c3d.tgz docker-compose.local.yml

Environment:
  SERVICE_NAME   Compose service to recreate (default: sub2api)
  COMPOSE_FILE   Compose file(s) used by Docker Compose when [compose-file] is omitted
EOF
}

command_exists() {
    command -v "$1" >/dev/null 2>&1
}

compose() {
    if docker compose version >/dev/null 2>&1; then
        docker compose "$@"
    elif command_exists docker-compose; then
        docker-compose "$@"
    else
        print_error "Docker Compose is not installed. Install docker compose or docker-compose first."
        exit 1
    fi
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
    usage
    exit 0
fi

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
    usage
    exit 1
fi

archive_arg="$1"
compose_arg="${2:-}"
service_name="${SERVICE_NAME:-sub2api}"

if ! command_exists docker; then
    print_error "docker is not installed. Install Docker first."
    exit 1
fi

if ! command_exists tar; then
    print_error "tar is not installed."
    exit 1
fi

if [ ! -f "$archive_arg" ]; then
    print_error "Image archive not found: $archive_arg"
    exit 1
fi

archive_dir="$(cd "$(dirname "$archive_arg")" && pwd)"
archive_path="${archive_dir}/$(basename "$archive_arg")"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd "$script_dir"

compose_args=()
compose_label=""
if [ -n "$compose_arg" ]; then
    if [ ! -f "$compose_arg" ]; then
        print_error "Compose file not found: $compose_arg"
        exit 1
    fi
    compose_args=(-f "$compose_arg")
    compose_label="$compose_arg"
elif [ -n "${COMPOSE_FILE:-}" ]; then
    compose_label="COMPOSE_FILE=${COMPOSE_FILE}"
elif [ -f "docker-compose.yml" ] && [ -f "docker-compose.local.yml" ]; then
    print_error "Both docker-compose.yml and docker-compose.local.yml were found."
    print_error "Pass the compose file explicitly, for example: ./recreate.sh <archive.tgz> docker-compose.local.yml"
    exit 1
elif [ -f "docker-compose.yml" ]; then
    compose_args=(-f docker-compose.yml)
    compose_label="docker-compose.yml"
elif [ -f "docker-compose.local.yml" ]; then
    compose_args=(-f docker-compose.local.yml)
    compose_label="docker-compose.local.yml"
else
    print_error "No docker-compose.yml or docker-compose.local.yml found in $script_dir"
    exit 1
fi

print_info "Using compose configuration: ${compose_label}"

if ! compose "${compose_args[@]}" config --services | grep -qx "$service_name"; then
    print_error "Compose service not found: $service_name"
    exit 1
fi

service_image="$(compose "${compose_args[@]}" config | awk -v service="$service_name" '
    $0 == "services:" {
        in_services = 1
        next
    }
    in_services && $0 !~ /^  / {
        in_services = 0
    }
    in_services && $0 == "  " service ":" {
        in_service = 1
        next
    }
    in_service && $0 ~ /^  [^[:space:]][^:]*:/ {
        in_service = 0
    }
    in_service && $0 ~ /^    image:[[:space:]]*/ {
        sub(/^    image:[[:space:]]*/, "")
        gsub(/^"|"$/, "")
        gsub(/^'\''|'\''$/, "")
        print
        exit
    }
')"

if [ -z "$service_image" ]; then
    print_error "Could not determine image for compose service: $service_name"
    exit 1
fi

print_info "Compose service image: $service_image"

tmp_dir="$(mktemp -d)"
cleanup() {
    rm -rf "$tmp_dir"
}
trap cleanup EXIT

load_input="$archive_path"
listing_file="$tmp_dir/archive.list"

if tar -tzf "$archive_path" >"$listing_file" 2>/dev/null && grep -Eq '(^|/)sub2api\.tar$' "$listing_file"; then
    print_info "Extracting docker-export package: $archive_path"
    tar -xzf "$archive_path" -C "$tmp_dir"
    load_input="$(find "$tmp_dir" -type f -name sub2api.tar -print -quit)"
    if [ -z "$load_input" ]; then
        print_error "sub2api.tar was listed but could not be extracted from: $archive_path"
        exit 1
    fi
else
    print_warning "Archive does not contain sub2api.tar; trying docker load directly."
fi

print_info "Loading Docker image..."
if ! load_output="$(docker load -i "$load_input" 2>&1)"; then
    printf '%s\n' "$load_output" >&2
    print_error "Docker image load failed."
    exit 1
fi
printf '%s\n' "$load_output"

loaded_image="$(printf '%s\n' "$load_output" | awk -F': ' '/^Loaded image: / { print $2; exit }')"
loaded_image_id="$(printf '%s\n' "$load_output" | awk -F': ' '/^Loaded image ID: / { print $2; exit }')"

if [ -n "$loaded_image" ]; then
    if [ "$loaded_image" != "$service_image" ]; then
        print_warning "Loaded image tag is $loaded_image, but compose uses $service_image. Retagging..."
        docker tag "$loaded_image" "$service_image"
    fi
elif [ -n "$loaded_image_id" ]; then
    print_warning "Loaded image has no repository tag. Tagging $loaded_image_id as $service_image..."
    docker tag "$loaded_image_id" "$service_image"
else
    print_error "Could not determine which image was loaded by docker load."
    exit 1
fi

print_info "Recreating compose service: $service_name"
compose "${compose_args[@]}" up -d --no-deps --force-recreate "$service_name"

print_success "Applied $archive_path and recreated $service_name."
