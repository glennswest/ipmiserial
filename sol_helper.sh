#!/bin/sh
# SOL helper - runs ipmitool with PTY isolated from main process
# Usage: sol_helper.sh <server_name> <ip> <username> <password> <log_file>

SERVER="$1"
IP="$2"
USER="$3"
PASS="$4"
LOGFILE="$5"

if [ -z "$LOGFILE" ]; then
    echo "Usage: $0 <server_name> <ip> <username> <password> <log_file>"
    exit 1
fi

# Create log directory if needed
mkdir -p "$(dirname "$LOGFILE")"

# Deactivate any existing session first
ipmitool -I lanplus -H "$IP" -U "$USER" -P "$PASS" -R 1 -N 3 sol deactivate 2>/dev/null

# Run SOL with script providing PTY, append to log file
# script -q -c "command" /dev/null provides PTY and outputs to stdout
exec script -q -c "ipmitool -I lanplus -H '$IP' -U '$USER' -P '$PASS' -R 2 -N 5 sol activate" /dev/null >> "$LOGFILE" 2>&1
