#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

if [[ $# -lt 1 ]]; then
  echo "usage: bash scripts/log-scrub.sh <log-dir-or-file> [<log-dir-or-file> ...]" >&2
  exit 2
fi

TARGETS=("$@")

PATTERN_NAMES=(
  "bearer_header"
  "json_token_field"
  "form_token_field"
  "plain_token_field"
  "jwt_shape"
  "openai_api_key"
)
PATTERN_REGEXES=(
  'Bearer [A-Za-z0-9+/=_-]{20,}'
  '"(access_token|refresh_token|id_token)"[[:space:]]*:[[:space:]]*"[^"]{8,}"'
  '(access_token|refresh_token|id_token)[[:space:]]*=[[:space:]]*"[^"]{8,}"'
  '(^|[^A-Za-z0-9_])(access_token|refresh_token|id_token)[[:space:]]*=[[:space:]]*[^"[:space:]][^[:space:]]{7,}'
  'eyJ[A-Za-z0-9._-]{40,}'
  'sk-[A-Za-z0-9_-]{8,}'
)
PATTERN_ALLOW_REGEXES=(
  ''
  '"(access_token|refresh_token|id_token)"[[:space:]]*:[[:space:]]*"(fixture_[a-z0-9_]+|\[redacted\])"'
  '(access_token|refresh_token|id_token)[[:space:]]*=[[:space:]]*"(fixture_[a-z0-9_]+|\[redacted\])"'
  '(^|[^A-Za-z0-9_])(access_token|refresh_token|id_token)[[:space:]]*=[[:space:]]*(fixture_[a-z0-9_]+|\[redacted\])'
  ''
  ''
)

LOG_FILES=()
DB_FILES=()
for target in "${TARGETS[@]}"; do
  if [[ -d "${target}" ]]; then
    while IFS= read -r file; do
      LOG_FILES+=("${file}")
    done < <(find "${target}" -type f \( -name '*.log' -o -name '*.txt' -o -name '*.json' -o -name '*.html' -o -name '*.htm' \) | sort)
    while IFS= read -r file; do
      DB_FILES+=("${file}")
    done < <(find "${target}" -type f \( -name '*.db' -o -name '*.sqlite' -o -name '*.sqlite3' \) | sort)
    continue
  fi
  if [[ -f "${target}" ]]; then
    case "${target}" in
      *.db|*.sqlite|*.sqlite3) DB_FILES+=("${target}") ;;
      *) LOG_FILES+=("${target}") ;;
    esac
    continue
  fi
  echo "log-scrub: target not found: ${target}" >&2
  exit 2
done

DB_DUMP=""
if [[ "${#DB_FILES[@]}" -gt 0 ]]; then
  if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "log-scrub: sqlite3 is required to scan request_records in DB files: ${DB_FILES[*]}" >&2
    exit 2
  fi
  DB_DUMP="$(mktemp "${TMPDIR:-/tmp}/log-scrub-db.XXXXXX.txt")"
  trap 'rm -f "${DB_DUMP}"' EXIT
  for db in "${DB_FILES[@]}"; do
    if sqlite3 "${db}" "SELECT name FROM sqlite_master WHERE type='table' AND name='request_records';" 2>/dev/null | grep -qx 'request_records'; then
      {
        echo "== ${db}:request_records =="
        sqlite3 -batch -noheader -separator ' ' "${db}" \
          "SELECT COALESCE(client_request_body,''), COALESCE(upstream_request_body,''), COALESCE(upstream_response_body,''), COALESCE(model_params,''), COALESCE(token_usage,'') FROM request_records;"
      } >> "${DB_DUMP}"
    fi
    if sqlite3 "${db}" "SELECT name FROM sqlite_master WHERE type='table' AND name='upstream_accounts';" 2>/dev/null | grep -qx 'upstream_accounts'; then
      {
        echo "== ${db}:upstream_accounts =="
        sqlite3 -batch -noheader -separator ' ' "${db}" \
          "SELECT COALESCE(api_key,''), COALESCE(CAST(access_token AS TEXT),''), COALESCE(CAST(refresh_token AS TEXT),''), COALESCE(CAST(id_token AS TEXT),'') FROM upstream_accounts;"
      } >> "${DB_DUMP}"
    fi
  done
  if [[ -s "${DB_DUMP}" ]]; then
    LOG_FILES+=("${DB_DUMP}")
  fi
fi

if [[ "${#LOG_FILES[@]}" -eq 0 ]]; then
  echo "log-scrub: no scannable log/artifact/DB files found under: ${TARGETS[*]}" >&2
  exit 2
fi

failures=0
for idx in "${!PATTERN_NAMES[@]}"; do
  name="${PATTERN_NAMES[$idx]}"
  regex="${PATTERN_REGEXES[$idx]}"
  allow_regex="${PATTERN_ALLOW_REGEXES[$idx]}"
  matches="$(
    grep -InoE --binary-files=without-match -- "${regex}" "${LOG_FILES[@]}" \
      || true
  )"
  filtered_matches=""
  if [[ -n "${matches}" ]]; then
    while IFS= read -r match; do
      [[ -z "${match}" ]] && continue
      match_fragment="${match#*:}"
      match_fragment="${match_fragment#*:}"
      if [[ -n "${allow_regex}" ]] && printf '%s\n' "${match_fragment}" | grep -Eq -- "${allow_regex}"; then
        continue
      fi
      filtered_matches+="${match}"$'\n'
    done <<< "${matches}"
  fi
  matches="${filtered_matches%$'\n'}"
  if [[ -n "${matches}" ]]; then
    failures=1
    echo "log-scrub: matched forbidden pattern '${name}'" >&2
    echo "${matches}" >&2
  fi
done

if [[ "${failures}" -ne 0 ]]; then
  echo "log-scrub: token-like material detected under ${TARGETS[*]}" >&2
  exit 1
fi

echo "log-scrub: clean (${#LOG_FILES[@]} files across ${TARGETS[*]})"
