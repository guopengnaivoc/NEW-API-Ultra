#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

lock_dir="$repo_root/.env.lock"
if ! mkdir "$lock_dir" 2>/dev/null; then
  printf '%s\n' 'another .env generation is already in progress; refusing to race it.' >&2
  exit 1
fi

tmp_env=''
cleanup() {
  if [[ -n "$tmp_env" ]]; then
    rm -f "$tmp_env"
  fi
  rmdir "$lock_dir" 2>/dev/null || true
}
trap cleanup EXIT

if [[ -e .env ]]; then
  printf '%s\n' '.env already exists; refusing to overwrite it.' >&2
  exit 1
fi

command -v openssl >/dev/null 2>&1 || {
  printf '%s\n' 'openssl is required to generate deployment secrets.' >&2
  exit 1
}

umask 077
hex_secret() {
  local value
  value="$(openssl rand -hex 32)" || return 1
  [[ "$value" =~ ^[0-9a-f]{64}$ ]] || return 1
  printf '%s' "$value"
}

base64_key() {
  local value
  value="$(openssl rand -base64 32)" || return 1
  [[ "$value" =~ ^[A-Za-z0-9+/]{43}=$ ]] || return 1
  printf '%s' "$value"
}

if ! postgres_password="$(hex_secret)" \
  || ! redis_password="$(hex_secret)" \
  || ! session_secret="$(hex_secret)" \
  || ! crypto_secret="$(hex_secret)" \
  || ! data_key="$(base64_key)"; then
  printf '%s\n' 'openssl returned an invalid or incomplete secret; refusing to create .env.' >&2
  exit 1
fi

tmp_env="$(mktemp "$repo_root/.env.tmp.XXXXXX")"
chmod 600 "$tmp_env"
cat > "$tmp_env" <<EOF
# Generated locally by scripts/bootstrap-env.sh. Do not commit or share this file.
POSTGRES_PASSWORD=${postgres_password}
REDIS_PASSWORD=${redis_password}
SESSION_SECRET=${session_secret}
CRYPTO_SECRET=${crypto_secret}
DATA_ENCRYPTION_KEYS=primary=${data_key}
DATA_ENCRYPTION_ACTIVE_KEY_ID=primary
SESSION_COOKIE_SECURE=false
# For public HTTPS deployments, set SESSION_COOKIE_SECURE=true and list exact origins:
# SESSION_COOKIE_TRUSTED_URL=https://example.example
EOF

# Do not replace a file that appeared after the initial existence check. The
# temporary file also prevents a failed random-source command from leaving a
# partial .env behind.
mv -n "$tmp_env" .env
if [[ -e "$tmp_env" ]]; then
  printf '%s\n' '.env appeared while generating secrets; refusing to overwrite it.' >&2
  exit 1
fi
rmdir "$lock_dir"
trap - EXIT

printf 'Created %s/.env with fresh secrets. Back it up securely before deployment.\n' "$repo_root"
