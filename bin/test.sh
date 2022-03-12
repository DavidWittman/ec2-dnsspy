#!/bin/bash

set -euo pipefail

# AL2 in us-east-2
AMI_ID=ami-0dfb4b2fe71065a95
INSTANCE_TYPE=t4g.nano
DOMAIN="$(cat /proc/sys/kernel/random/uuid).com"

echo "Random domain is $DOMAIN"
echo -n "Creating EC2 instance... "
INSTANCE_ID=$(aws ec2 run-instances \
    --image-id="$AMI_ID" \
    --count 1 \
    --instance-type="$INSTANCE_TYPE" \
    --user-data '#!/bin/sh
sleep 15
curl --retry 3 --silent '"$DOMAIN"'
shutdown -h now' \
    --instance-initiated-shutdown-behavior terminate \
    --output text \
    --query 'Instances[0].InstanceId')

echo "$INSTANCE_ID"

./ec2-dnsspy -i "$INSTANCE_ID"
