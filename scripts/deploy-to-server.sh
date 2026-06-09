#!/bin/bash
# deploy-to-server.sh - Deploy stb-manager to remote server
# Usage: ./deploy-to-server.sh <server>

set -e

SERVER="${1:-root@70.39.197.80}"
SSH_CMD="ssh -o StrictHostKeyChecking=no ${SERVER}"
SCP_CMD="scp -o StrictHostKeyChecking=no"

echo "=== Deploying stb-manager to ${SERVER} ==="

# Deploy compose files
${SCP_CMD} docker-compose.yml "${SERVER}:/opt/stb-manager/docker-compose.yml"
${SCP_CMD} docker-compose-stb.yml "${SERVER}:/opt/stb-manager/docker-compose-stb.yml"

if [ -f docker-compose-watchtower.yml ]; then
    ${SCP_CMD} docker-compose-watchtower.yml "${SERVER}:/opt/stb-manager/docker-compose-watchtower.yml"
fi

echo "=== Connecting to server for update ==="

${SSH_CMD} '
cd /opt/stb-manager && \
echo "Pulling latest image..." && \
docker compose pull stb-manager && \
echo "Restarting stb-manager..." && \
docker compose up -d stb-manager && \
echo "Cleaning up old images..." && \
docker image prune -f && \
echo "" && \
echo "=== Deploy Complete ===" && \
docker compose ps stb-manager && \
docker logs --tail 5 stb-manager
'
