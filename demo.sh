#!/usr/bin/env bash
set -euo pipefail

LB_URL="http://localhost:8080"
B1="http://localhost:18081"
B2="http://localhost:18082"
B3="http://localhost:18083"

bold() { printf "\033[1m%s\033[0m\n" "$*"; }
hr()   { printf "\n——— %s ———\n\n" "$*"; }

hit_once() {
  local url="$1"
  curl -s --max-time 2 "$url" | head -n 2
}

tally_lb() {
  local n="${1:-30}"
  declare -A counts=( ["backend-1"]=0 ["backend-2"]=0 ["backend-3"]=0 )
  local fail=0

  for i in $(seq 1 "$n"); do
    # capture first line, extract backend name like backend-1
    line="$(curl -s --max-time 3 "$LB_URL" | head -n 1 || true)"
    name="$(grep -oE 'backend-[0-9]+' <<<"$line" || true)"
    if [[ -n "$name" ]]; then
      counts["$name"]=$(( ${counts[$name]} + 1 ))
      printf "LB %2d -> %s\n" "$i" "$name"
    else
      printf "LB %2d -> FAIL (%s)\n" "$i" "$line"
      fail=$((fail+1))
    fi
    sleep 0.05
  done

  echo
  bold "LB distribution (out of $n):"
  printf "  backend-1: %d\n" "${counts[backend-1]}"
  printf "  backend-2: %d\n" "${counts[backend-2]}"
  printf "  backend-3: %d\n" "${counts[backend-3]}"
  printf "  failures : %d\n" "$fail"
}

tally_direct_random() {
  local n="${1:-30}"
  declare -A counts=( ["backend-1"]=0 ["backend-2"]=0 ["backend-3"]=0 )
  local fail=0
  local urls=("$B1" "$B2" "$B3")

  for i in $(seq 1 "$n"); do
    # pick a backend at random (what an app would have to do without an LB)
    url="${urls[$RANDOM % 3]}"
    line="$(curl -s --max-time 2 "$url" | head -n 1 || true)"

    name="$(grep -oE 'backend-[0-9]+' <<<"$line" || true)"
    if [[ -n "$name" ]]; then
      counts["$name"]=$(( ${counts[$name]} + 1 ))
      printf "DIRECT %2d -> %-9s (%s)\n" "$i" "$name" "$url"
    else
      printf "DIRECT %2d -> FAIL          (%s)\n" "$i" "$url"
      fail=$((fail+1))
    fi
    sleep 0.05
  done

  echo
  bold "Direct (no LB) distribution (out of $n):"
  printf "  backend-1: %d\n" "${counts[backend-1]}"
  printf "  backend-2: %d\n" "${counts[backend-2]}"
  printf "  backend-3: %d\n" "${counts[backend-3]}"
  printf "  failures : %d\n" "$fail"
}

ensure_up() {
  docker compose ps --format 'table {{.Name}}\t{{.Status}}\t{{.Ports}}'
}

main() {
  hr "Check services"
  ensure_up

  hr "Warm up (hit each backend directly once)"
  echo "# backend1"; hit_once "$B1"; echo
  echo "# backend2"; hit_once "$B2"; echo
  echo "# backend3"; hit_once "$B3"; echo

  hr "Without LB: random direct calls to backends"
  tally_direct_random 30

  hr "With LB: single URL, LB spreads traffic (round-robin + health)"
  tally_lb 30

  hr "Simulate failure: stop backend2"
  docker stop backend2 >/dev/null
  sleep 3
  echo "(backend2 stopped)"

  hr "Without LB during failure: many direct calls to backend2 will FAIL"
  tally_direct_random 30

  hr "With LB during failure: traffic continues (backend-1 & backend-3 only)"
  tally_lb 30

  hr "Recover: start backend2"
  docker start backend2 >/dev/null
  echo "waiting for health check…"
  sleep 5

  hr "With LB after recovery: all 3 receive traffic again"
  tally_lb 30

  hr "Done"
}

main "$@"
