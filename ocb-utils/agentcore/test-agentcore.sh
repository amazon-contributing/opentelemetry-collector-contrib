#!/bin/bash
# E2E test for agentcore collector: start collector, send telemetry, verify in CloudWatch.
# Usage: ./test-agentcore.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY="$SCRIPT_DIR/output/otelcol-agentcore"
CONFIG="$SCRIPT_DIR/config.yaml"
COLLECTOR_LOG="$SCRIPT_DIR/output/collector-test.log"

# CloudWatch defaults (must match config.yaml)
REGION="${AWS_REGION:-us-west-2}"
APP_LOG_GROUP="${AWS_APP_LOG_GROUP:-AgentCoreAppLogs}"
APP_LOG_STREAM="${AWS_APP_LOG_STREAM:-default}"
EMF_LOG_GROUP="${AWS_EMF_LOG_GROUP:-AgentCoreEMF}"
EMF_LOG_STREAM="${AWS_EMF_LOG_STREAM:-default}"
SPANS_LOG_GROUP="aws/spans"
SPANS_LOG_STREAM="default"

COLLECTOR_PID=""
PASS_COUNT=0
FAIL_COUNT=0

cleanup() {
    if [ -n "$COLLECTOR_PID" ] && kill -0 "$COLLECTOR_PID" 2>/dev/null; then
        echo ""
        echo "Stopping collector (PID $COLLECTOR_PID)..."
        kill "$COLLECTOR_PID" 2>/dev/null
        wait "$COLLECTOR_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

log()  { echo "[$(date '+%H:%M:%S')] $*"; }
pass() { log "PASS: $1"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail() { log "FAIL: $1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }

# ── Step 1: Pre-flight checks ──
log "Step 1/7: Pre-flight checks"

if [ ! -x "$BINARY" ]; then
    echo "ERROR: Binary not found at $BINARY. Run ./build-agentcore.sh first."
    exit 1
fi

if ! aws sts get-caller-identity --region "$REGION" >/dev/null 2>&1; then
    echo "ERROR: AWS credentials not configured. Run 'aws configure' or set AWS_PROFILE."
    exit 1
fi

if command -v lsof &>/dev/null && lsof -i :4317 -i :4318 >/dev/null 2>&1; then
    echo "ERROR: Ports 4317/4318 already in use. Stop existing collector first."
    exit 1
fi

log "  Binary: $BINARY"
log "  Region: $REGION"
log "  Checks passed"

# ── Step 2: Ensure CW log groups/streams exist ──
log "Step 2/7: Ensuring CloudWatch log groups and streams exist"

for lg in "$APP_LOG_GROUP" "$EMF_LOG_GROUP"; do
    aws logs create-log-group --log-group-name "$lg" --region "$REGION" 2>/dev/null || true
    aws logs create-log-stream --log-group-name "$lg" --log-stream-name default --region "$REGION" 2>/dev/null || true
done
log "  Log groups ready: $APP_LOG_GROUP, $EMF_LOG_GROUP"

# ── Step 3: Record test start time ──
# Use milliseconds epoch for CW filter-log-events --start-time
TEST_START_MS=$(($(date +%s) * 1000))
log "  Test start timestamp: $TEST_START_MS"

# ── Step 4: Start collector ──
log "Step 3/7: Starting collector"
"$BINARY" --config "$CONFIG" > "$COLLECTOR_LOG" 2>&1 &
COLLECTOR_PID=$!
log "  Collector PID: $COLLECTOR_PID"

# Wait for ready
READY=false
for i in $(seq 1 30); do
    if grep -q "Everything is ready" "$COLLECTOR_LOG" 2>/dev/null; then
        READY=true
        break
    fi
    sleep 1
done

if [ "$READY" = false ]; then
    echo "ERROR: Collector did not become ready within 30s. Log tail:"
    tail -20 "$COLLECTOR_LOG"
    exit 1
fi
log "  Collector is ready"

# ── Step 4b: Ensure Python venv and dependencies ──
if [ ! -d "$SCRIPT_DIR/venv" ]; then
    log "  Creating Python venv..."
    python3 -m venv "$SCRIPT_DIR/venv"
fi
(
    cd "$SCRIPT_DIR"
    source venv/bin/activate
    pip install -q -r sample-app/requirements.txt 2>/dev/null
)

# ── Step 5: Send telemetry ──
log "Step 4/7: Sending telemetry data"
(
    cd "$SCRIPT_DIR"
    source venv/bin/activate
    python3 sample-app/send_telemetry.py
)
log "  Telemetry sent"

# ── Step 6: Wait for CW ingestion ──
log "Step 5/7: Waiting for CloudWatch ingestion (15s)"
sleep 15

# ── Step 7: Verify data in CloudWatch ──
log "Step 6/7: Verifying data in CloudWatch"

# Helper: query CW log group for events since test start, check for pattern
verify_log_group() {
    local log_group="$1"
    local pattern="$2"
    local label="$3"

    local result
    result=$(aws logs filter-log-events \
        --log-group-name "$log_group" \
        --start-time "$TEST_START_MS" \
        --region "$REGION" \
        --limit 20 \
        --output json 2>&1) || true

    local event_count
    event_count=$(echo "$result" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('events',[])))" 2>/dev/null || echo "0")

    if [ "$event_count" -gt 0 ]; then
        # Check pattern in events
        local match_count
        match_count=$(echo "$result" | python3 -c "
import sys, json
data = json.load(sys.stdin)
pattern = '$pattern'
count = sum(1 for e in data.get('events', []) if pattern in e.get('message', ''))
print(count)
" 2>/dev/null || echo "0")

        if [ "$match_count" -gt 0 ]; then
            pass "$label: $match_count matching events in $log_group ($event_count total)"
            return 0
        else
            fail "$label: $event_count events found in $log_group but none matched pattern '$pattern'"
            return 1
        fi
    else
        fail "$label: No events found in $log_group since test start"
        return 1
    fi
}

# 7a: Verify application logs
verify_log_group "$APP_LOG_GROUP" "Processed request" "Logs"

# 7b: Verify EMF metrics
verify_log_group "$EMF_LOG_GROUP" "app.request.count" "Metrics (EMF)"

# 7c: Verify spans
verify_log_group "$SPANS_LOG_GROUP" "sample-test-app" "Traces (Spans)"

# ── Step 8: Check collector log for export errors ──
log "Step 7/7: Checking collector log for errors"
ERROR_COUNT=$(grep -c "Exporting failed" "$COLLECTOR_LOG" 2>/dev/null || true)
ERROR_COUNT=${ERROR_COUNT:-0}
# grep -c can return multiline if file has multiple matches; take first line
ERROR_COUNT=$(echo "$ERROR_COUNT" | head -1)
if [ "$ERROR_COUNT" -eq 0 ]; then
    pass "No export errors in collector log"
else
    fail "Found $ERROR_COUNT export errors in collector log"
    grep "Exporting failed" "$COLLECTOR_LOG" | head -3
fi

# ── Summary ──
echo ""
echo "=========================================="
echo "  Test Summary"
echo "=========================================="
echo "  PASS: $PASS_COUNT"
echo "  FAIL: $FAIL_COUNT"
echo "=========================================="

if [ "$FAIL_COUNT" -gt 0 ]; then
    echo "  Result: FAILED"
    echo ""
    echo "  Collector log: $COLLECTOR_LOG"
    exit 1
else
    echo "  Result: ALL PASSED"
    exit 0
fi
