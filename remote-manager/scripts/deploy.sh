#!/bin/bash
# deploy.sh - Deploy gk-agent to the ROM
# Usage: ./deploy.sh

AGENT="remote-manager/agent/gk-agent-arm"
ROM_DIR="system"

if [ ! -f "$AGENT" ]; then
    echo "ERROR: $AGENT not found. Run 'go build -o agent/gk-agent-arm' first."
    exit 1
fi

# Deploy agent binary
cp "$AGENT" "$ROM_DIR/system/system/bin/gk-agent"
chmod 0755 "$ROM_DIR/system/system/bin/gk-agent"
echo "Deployed gk-agent to system/system/system/bin/gk-agent"

# Create init service definition
mkdir -p "$ROM_DIR/system/system/etc/init"
cat > "$ROM_DIR/system/system/etc/init/gk-agent.rc" << 'EOF'
service gk-agent /system/bin/gk-agent
    class main
    user root
    group root net_bt_admin
    oneshot

    environment MQTT_BROKER=tcp://192.168.1.100:1883
    environment MQTT_USER=admin
    environment MQTT_PASS=admin@manager
    environment DEVICE_SERIAL=GK6323V100C-UNKNOWN
EOF
echo "Created gk-agent.rc in system/system/etc/init/"

echo ""
echo "=== Deployment Complete ==="
echo "Next steps:"
echo "1. Add service start to init.kunlun.rc or init.rc"
echo "2. Rebuild the ROM package"
echo "3. Flash to device"
