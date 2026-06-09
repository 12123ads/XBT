#!/usr/bin/env bash
set -euo pipefail

DEPLOY_BRANCH="${DEPLOY_BRANCH:-main}"
HTTP_PROXY="${HTTP_PROXY:-http://10.0.0.170:7890}"
HTTPS_PROXY="${HTTPS_PROXY:-$HTTP_PROXY}"
NO_PROXY="${NO_PROXY:-localhost,127.0.0.1,db,server,web}"

export DOCKER_BUILDKIT="${DOCKER_BUILDKIT:-1}"

echo "Pulling latest code from origin/$DEPLOY_BRANCH..."
git fetch origin "$DEPLOY_BRANCH"
if git show-ref --verify --quiet "refs/heads/$DEPLOY_BRANCH"; then
  git switch "$DEPLOY_BRANCH"
else
  git switch -c "$DEPLOY_BRANCH" --track "origin/$DEPLOY_BRANCH"
fi
git branch --set-upstream-to="origin/$DEPLOY_BRANCH" "$DEPLOY_BRANCH" >/dev/null
git pull --ff-only

echo "Building server and web images with proxy:"
echo "  HTTP_PROXY=$HTTP_PROXY"
echo "  HTTPS_PROXY=$HTTPS_PROXY"
echo "  NO_PROXY=$NO_PROXY"

docker compose build server web \
  --build-arg "HTTP_PROXY=$HTTP_PROXY" \
  --build-arg "HTTPS_PROXY=$HTTPS_PROXY" \
  --build-arg "NO_PROXY=$NO_PROXY" \
  --build-arg "http_proxy=$HTTP_PROXY" \
  --build-arg "https_proxy=$HTTPS_PROXY" \
  --build-arg "no_proxy=$NO_PROXY"

docker compose up -d db server web
docker compose ps
