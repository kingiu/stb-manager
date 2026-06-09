#!/bin/bash
AGENT="../remote-manager/agent/gk-agent-arm"
ROM_DIR="system"

if [ ! -f "$AGENT" ]; then
    echo "ERROR: $AGENT not found"
    exit 1
fi

cp "$AGENT" "$ROM_DIR/system/system/bin/gk-agent"
chmod 0755 "$ROM_DIR/system/system/bin/gk-agent"
echo "Deployed gk-agent to system/system/system/bin/gk-agent"
echo "Done."
