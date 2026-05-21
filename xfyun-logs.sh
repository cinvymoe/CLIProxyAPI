#!/bin/bash
# xfyun-logs.sh - View and analyze xunfei (xfyun) model error logs from CLIProxyAPI
# Usage:
#   ./xfyun-logs.sh              # Last 1 hour, summary + errors
#   ./xfyun-logs.sh -t 6h        # Last 6 hours
#   ./xfyun-logs.sh -t 24h       # Last 24 hours
#   ./xfyun-logs.sh -t 3d        # Last 3 days
#   ./xfyun-logs.sh --errors     # Only show error lines (no summary)
#   ./xfyun-logs.sh --trace ID   # Trace a specific request ID
#   ./xfyun-logs.sh --watch      # Live tail xfyun logs

set -euo pipefail

CONTAINER="cli-proxy-api"
MODEL="astron-code-latest-xfyun"
PROVIDER="xfyun-coding"
TIME_RANGE="1h"
MODE="summary"
TRACE_ID=""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

while [[ $# -gt 0 ]]; do
    case "$1" in
        -t|--time)
            TIME_RANGE="$2"
            shift 2
            ;;
        --errors)
            MODE="errors"
            shift
            ;;
        --trace)
            MODE="trace"
            TRACE_ID="$2"
            shift 2
            ;;
        --watch)
            MODE="watch"
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [-t TIME] [--errors] [--trace ID] [--watch]"
            echo "  -t TIME       Time range (1h, 6h, 24h, 3d, etc.)"
            echo "  --errors      Only show error lines, no summary"
            echo "  --trace ID    Trace a specific request ID"
            echo "  --watch       Live tail xfyun logs"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

get_logs() {
    docker logs "$CONTAINER" --since "$TIME_RANGE" 2>&1
}

xfyun_logs() {
    get_logs | grep -E "$MODEL|$PROVIDER"
}

if [[ "$MODE" == "trace" ]]; then
    if [[ -z "$TRACE_ID" ]]; then
        echo -e "${RED}Error: --trace requires a request ID${NC}"
        exit 1
    fi
    echo -e "${BOLD}Tracing request $TRACE_ID:${NC}"
    get_logs | grep "$TRACE_ID"
    exit 0
fi

if [[ "$MODE" == "watch" ]]; then
    echo -e "${BOLD}Live tailing xfyun logs (Ctrl+C to stop)...${NC}"
    docker logs -f "$CONTAINER" 2>&1 | grep --line-buffered -E "$MODEL|$PROVIDER"
    exit 0
fi

if [[ "$MODE" == "errors" ]]; then
    echo -e "${BOLD}xfyun error logs (last $TIME_RANGE):${NC}"
    xfyun_logs | grep -E "error|warn|500|503|quota|Suspended|payment_required" | \
        while IFS= read -r line; do
            if echo "$line" | grep -q "503"; then
                echo -e "${YELLOW}${line}${NC}"
            elif echo "$line" | grep -q "500"; then
                echo -e "${RED}${line}${NC}"
            elif echo "$line" | grep -q "quota exceeded"; then
                echo -e "${CYAN}${line}${NC}"
            elif echo "$line" | grep -q "Suspended"; then
                echo -e "${YELLOW}${line}${NC}"
            elif echo "$line" | grep -q "payment_required"; then
                echo -e "${RED}${line}${NC}"
            else
                echo "$line"
            fi
        done
    exit 0
fi

echo -e "${BOLD}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  xfyun (astron-code-latest-xfyun) Log Report${NC}"
echo -e "${BOLD}  Time range: last $TIME_RANGE${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════════════════${NC}"

LOGS=$(xfyun_logs)

if [[ -z "$LOGS" ]]; then
    echo -e "${GREEN}No xfyun-related logs found in the last $TIME_RANGE.${NC}"
    exit 0
fi

TOTAL_REQUESTS=$(echo "$LOGS" | grep -c "Use API key" || true)
echo -e "\n${BOLD}1. Request Volume${NC}"
echo "   Total requests: $TOTAL_REQUESTS"

echo -e "\n${BOLD}2. HTTP Response Status${NC}"
ALL_LOGS=$(get_logs)
XFYUN_IDS=$(echo "$LOGS" | grep "Use API key" | awk -F'[][]' '{print $4}' || true)

if [[ -n "$XFYUN_IDS" ]]; then
    STATUS_200=$(echo "$ALL_LOGS" | grep -F "$XFYUN_IDS" | grep -c "200" || true)
    STATUS_500=$(echo "$ALL_LOGS" | grep -F "$XFYUN_IDS" | grep -c "500" || true)
    STATUS_503=$(echo "$ALL_LOGS" | grep -F "$XFYUN_IDS" | grep -c "503" || true)
    STATUS_OTHER=$(echo "$ALL_LOGS" | grep -F "$XFYUN_IDS" | grep "gin_logger" | grep -vcE "200|500|503" || true)

    echo -e "   ${GREEN}200 OK:      $STATUS_200${NC}"
    echo -e "   ${RED}500 Error:   $STATUS_500${NC}"
    echo -e "   ${YELLOW}503 NoClient: $STATUS_503${NC}"
    if [[ "$STATUS_OTHER" -gt 0 ]]; then
        echo "   Other:       $STATUS_OTHER"
    fi
else
    echo "   (no request IDs found)"
fi

echo -e "\n${BOLD}3. Client Lifecycle Events${NC}"
QUOTA_COUNT=$(echo "$LOGS" | grep -c "quota exceeded" || true)
SUSPEND_COUNT=$(echo "$LOGS" | grep -c "Suspended" || true)
RESUME_COUNT=$(echo "$LOGS" | grep -c "Resumed" || true)
PAYMENT_COUNT=$(echo "$LOGS" | grep -c "payment_required" || true)

echo -e "   ${CYAN}Quota exceeded:  $QUOTA_COUNT${NC}"
echo -e "   ${YELLOW}Suspended:       $SUSPEND_COUNT${NC}"
echo -e "   ${GREEN}Resumed:         $RESUME_COUNT${NC}"
echo -e "   ${RED}Payment required: $PAYMENT_COUNT${NC}"

# Client info
echo -e "\n${BOLD}4. Client Details${NC}"
CLIENTS=$(echo "$LOGS" | grep "Registered client" | grep "$PROVIDER" || true)
if [[ -n "$CLIENTS" ]]; then
    echo "$CLIENTS" | while IFS= read -r line; do
        echo "   $line"
    done
else
    # Try to extract client from suspend/resume events
    CLIENT_ID=$(echo "$LOGS" | grep -m1 "Suspended\|Resumed" | grep -oP 'openai-compatibility:xfyun-coding:\K[a-f0-9]+' || true)
    if [[ -n "$CLIENT_ID" ]]; then
        echo "   Provider: openai-compatibility:$PROVIDER"
        echo "   Client:   $CLIENT_ID"
    fi
fi

# Error timeline (last 10 errors)
echo -e "\n${BOLD}5. Recent Errors (last 10)${NC}"
ERROR_LINES=$(echo "$ALL_LOGS" | grep -E "500|503" | grep "chat/completions" | tail -10 || true)
if [[ -n "$ERROR_LINES" ]]; then
    echo "$ERROR_LINES" | while IFS= read -r line; do
        if echo "$line" | grep -q "503"; then
            echo -e "   ${YELLOW}${line}${NC}"
        else
            echo -e "   ${RED}${line}${NC}"
        fi
    done
else
    echo -e "   ${GREEN}No errors found.${NC}"
fi

# Quota event timeline
echo -e "\n${BOLD}6. Quota Event Timeline${NC}"
QUOTA_EVENTS=$(echo "$LOGS" | grep -E "quota exceeded|Suspended|Resumed|payment_required" | tail -20 || true)
if [[ -n "$QUOTA_EVENTS" ]]; then
    echo "$QUOTA_EVENTS" | while IFS= read -r line; do
        if echo "$line" | grep -q "quota exceeded"; then
            echo -e "   ${CYAN}${line}${NC}"
        elif echo "$line" | grep -q "Suspended"; then
            echo -e "   ${YELLOW}${line}${NC}"
        elif echo "$line" | grep -q "Resumed"; then
            echo -e "   ${GREEN}${line}${NC}"
        elif echo "$line" | grep -q "payment_required"; then
            echo -e "   ${RED}${line}${NC}"
        else
            echo "   $line"
        fi
    done
else
    echo -e "   ${GREEN}No quota events found.${NC}"
fi

echo -e "\n${BOLD}═══════════════════════════════════════════════════════════════${NC}"