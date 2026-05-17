#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo "usage: $0 <url> <route> <requests> <expected-backend> <output-dir>" >&2
  exit 2
fi

url="$1"
route="$2"
requests="$3"
expected_backend="$4"
output_dir="$5"

rm -rf "$output_dir"
mkdir -p "$output_dir"

for index in $(seq 1 "$requests"); do
  (
    body_file="${output_dir}/body-${index}.txt"
    status_file="${output_dir}/status-${index}.txt"
    status="$(curl -sS -o "$body_file" -w "%{http_code}" \
      -H "X-Trino-User: local-e2e-contracts" \
      -H "X-Trino-XTrinode: ${route}" \
      "$url" || true)"
    printf "%s\n" "$status" > "$status_file"
  ) &
done

wait

count_status() {
  local want="$1"
  awk -v want="$want" '$0 == want { count++ } END { print count + 0 }' "${output_dir}"/status-*.txt
}

status_200="$(count_status 200)"
status_429="$(count_status 429)"
status_502="$(count_status 502)"
status_503="$(count_status 503)"
backend_body_count="$(
  {
    grep -hl "\"backend\":\"${expected_backend}\"" "${output_dir}"/body-*.txt 2>/dev/null || true
  } | wc -l | tr -d ' '
)"

cat > "${output_dir}/summary.txt" <<SUMMARY
total=${requests}
status_200=${status_200}
status_429=${status_429}
status_502=${status_502}
status_503=${status_503}
backend_body_count=${backend_body_count}
SUMMARY

cat "${output_dir}/summary.txt"
