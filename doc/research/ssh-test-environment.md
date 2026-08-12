# SSH Test Environment Setup

Reference: DFC056
Created: 2026-03-10
Last modified: 2026-03-20

[Back to Index](./README.md)

**Status:** Planning
**Date:** March 2026
**Purpose:** Local VM environment for testing `from ssh` and `from catalog` before implementation

## Overview

We need 2-3 lightweight VMs that we can SSH to, each holding shards of test data. This lets us test the full distributed pipeline locally before deploying to real servers.

## Option 1: LXD Containers (Recommended)

LXD containers are the lightest option — they share the host kernel, start in seconds, and use minimal resources. They behave like VMs from SSH's perspective.

### Setup

```bash
# Install LXD if not already present
sudo snap install lxd
lxd init --minimal

# Create containers
lxc launch ubuntu:24.04 ssql-node1
lxc launch ubuntu:24.04 ssql-node2
lxc launch ubuntu:24.04 ssql-node3

# Check they're running
lxc list
```

### Install ssql on each node

```bash
# Copy the ssql binary to each container
for node in ssql-node1 ssql-node2 ssql-node3; do
  lxc file push ssql $node/usr/local/bin/ssql
done

# Verify
for node in ssql-node1 ssql-node2 ssql-node3; do
  echo "$node: $(lxc exec $node -- ssql version)"
done
```

### Set up SSH access

```bash
# Install SSH server in each container
for node in ssql-node1 ssql-node2 ssql-node3; do
  lxc exec $node -- apt-get update -qq
  lxc exec $node -- apt-get install -y -qq openssh-server
  lxc exec $node -- systemctl enable ssh
  lxc exec $node -- systemctl start ssh
done

# Push your SSH public key
for node in ssql-node1 ssql-node2 ssql-node3; do
  lxc exec $node -- mkdir -p /root/.ssh
  lxc file push ~/.ssh/id_ed25519.pub $node/root/.ssh/authorized_keys
  lxc exec $node -- chmod 600 /root/.ssh/authorized_keys
done

# Get container IPs
lxc list -c n4 -f csv
```

### Configure SSH aliases

Add to `~/.ssh/config`:

```
Host ssql-node1
    HostName <IP from lxc list>
    User root
    StrictHostKeyChecking no

Host ssql-node2
    HostName <IP from lxc list>
    User root
    StrictHostKeyChecking no

Host ssql-node3
    HostName <IP from lxc list>
    User root
    StrictHostKeyChecking no

# Connection multiplexing for all nodes
Host ssql-node*
    ControlMaster auto
    ControlPath ~/.ssh/cm-%r@%h:%p
    ControlPersist 60
```

### Verify SSH works

```bash
ssh ssql-node1 'ssql version'
ssh ssql-node2 'ssql version'
ssh ssql-node3 'ssql version'
```

## Option 2: Multipass VMs

Multipass is Ubuntu's lightweight VM manager. Slightly heavier than LXD but fully isolated VMs.

```bash
# Install
sudo snap install multipass

# Create VMs (smallest size)
multipass launch -n ssql-node1 -c 1 -m 512M -d 2G
multipass launch -n ssql-node2 -c 1 -m 512M -d 2G
multipass launch -n ssql-node3 -c 1 -m 512M -d 2G

# Transfer ssql binary
for node in ssql-node1 ssql-node2 ssql-node3; do
  multipass transfer ssql $node:/usr/local/bin/ssql
  multipass exec $node -- chmod +x /usr/local/bin/ssql
done

# Set up SSH (multipass VMs already have SSH, just need your key)
for node in ssql-node1 ssql-node2 ssql-node3; do
  multipass exec $node -- bash -c "mkdir -p ~/.ssh && chmod 700 ~/.ssh"
  cat ~/.ssh/id_ed25519.pub | multipass exec $node -- bash -c "cat >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
done

# Get IPs
multipass list
```

## Test Data Setup

### Generate sharded data

Create a script that distributes test data across nodes, partitioned by date:

```bash
#!/bin/bash
# generate-test-shards.sh

# Generate 3 months of event data, one shard per node
for month in 01 02 03; do
  cat > /tmp/events-2025-${month}.csv << EOF
timestamp,service,status,duration,region
2025-${month}-01T10:00:00,auth,200,45,us-east
2025-${month}-05T11:30:00,api,500,1200,eu-west
2025-${month}-10T09:15:00,web,200,30,us-east
2025-${month}-15T14:00:00,auth,401,15,ap-south
2025-${month}-20T16:45:00,api,200,89,eu-west
2025-${month}-25T08:30:00,web,503,5000,us-east
EOF
done

# Distribute to nodes
lxc exec ssql-node1 -- mkdir -p /data/events
lxc exec ssql-node2 -- mkdir -p /data/events
lxc exec ssql-node3 -- mkdir -p /data/events

lxc file push /tmp/events-2025-01.csv ssql-node1/data/events/2025-01.csv
lxc file push /tmp/events-2025-02.csv ssql-node2/data/events/2025-02.csv
lxc file push /tmp/events-2025-03.csv ssql-node3/data/events/2025-03.csv
```

### Create the shard catalog

```bash
cat > /tmp/test-catalog.csv << 'EOF'
host,path,format,date_from,date_to
ssql-node1,/data/events/2025-01.csv,csv,2025-01-01,2025-01-31
ssql-node2,/data/events/2025-02.csv,csv,2025-02-01,2025-02-28
ssql-node3,/data/events/2025-03.csv,csv,2025-03-01,2025-03-31
EOF
```

### Add a lookup table for join testing

```bash
cat > /tmp/services.csv << 'EOF'
service,team,oncall_email
auth,identity,identity@example.com
api,platform,platform@example.com
web,frontend,frontend@example.com
EOF

lxc file push /tmp/services.csv ssql-node1/data/services.csv
```

## Test Scenarios

Once `from ssh` and `from catalog` are implemented, these are the scenarios to validate:

### Basic remote read

```bash
# Single remote file
ssql from ssh ssql-node1 /data/events/2025-01.csv | ssql to table

# With pipeline
ssql from ssh ssql-node1 /data/events/2025-01.csv \
  | ssql where -if status eq 500 | ssql to table
```

### Catalog read (all shards)

```bash
# Read all shards
ssql from catalog /tmp/test-catalog.csv | ssql to table

# With aggregation
ssql from catalog /tmp/test-catalog.csv \
  | ssql group-by -field service -count | ssql to table
```

### Partition pruning

```bash
# Should only read ssql-node2 (February)
ssql from catalog /tmp/test-catalog.csv \
  -if date ge 2025-02-01 -if date le 2025-02-28 \
  | ssql to table
```

### Push-down

```bash
# Filter remotely, aggregate locally
ssql from catalog /tmp/test-catalog.csv \
  -remote 'where -if status ge 500' \
  | ssql group-by -field service -count | ssql to table
```

### Merge ordering

```bash
# K-way merge across shards, ordered by timestamp
ssql from catalog /tmp/test-catalog.csv -merge timestamp | ssql to table
```

### Error handling

```bash
# Stop a node, verify -on-error skip works
lxc stop ssql-node2
ssql from catalog /tmp/test-catalog.csv -on-error skip -shard-field _shard \
  | ssql to table
lxc start ssql-node2
```

### Remote join

```bash
# Local data joined with remote lookup
ssql from ssh ssql-node1 /data/events/2025-01.csv \
  | ssql join <(ssql from ssh ssql-node1 /data/services.csv) -using service \
  | ssql to table
```

### Code generation

```bash
# Verify generated code compiles and runs
SSQLGO=1 ssql from ssh ssql-node1 /data/events/2025-01.csv \
  | ssql where -if status ge 500 \
  | ssql generate go > /tmp/remote_test.go

go run /tmp/remote_test.go
```

## Teardown

```bash
# LXD
lxc delete ssql-node1 ssql-node2 ssql-node3 --force

# Multipass
multipass delete ssql-node1 ssql-node2 ssql-node3
multipass purge
```

## Automation

Once the manual setup works, wrap it in a script:

```bash
# scripts/ssh-test-setup.sh — creates VMs, installs ssql, distributes data
# scripts/ssh-test-teardown.sh — destroys VMs
# scripts/ssh-test-run.sh — runs all test scenarios, reports pass/fail
```

These could also be run in CI with a self-hosted runner that has LXD available.
