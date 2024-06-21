#!/bin/bash

data="[{\"service\":\"${INSTANCE_SERVICE}\",\"namespace\":\"${INSTANCE_NAMESPACE}\",\"host\":\"${INSTANCE_IP}\",\"port\":\"${INSTANCE_PORT}\",\"weight\":0}]"
echo "${data}"
curl -H "X-Polaris-Token; ${POLARIS_TOKEN}" -H 'Content-Type: application/json' -X PUT -d "${data}" "http://${POLARIS_OPEN_API}/naming/v1/instances"
