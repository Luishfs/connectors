#!/bin/bash
#
set -e

echo "building and activating the testing catalog via build-and-activate.sh"

flowctl-admin api build \
  --directory "${TEST_DIR}"/builds \
  --build-id "${BUILD_ID}" \
  --network "flow-test" \
  --source "file://${TEST_DIR}/${CATALOG}" \
  --log.level info

flowctl-admin api activate \
  --build-id "${BUILD_ID}" \
  --network "flow-test" \
  --log.level info \
  --all
