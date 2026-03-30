#!/bin/bash
set -euo pipefail

#rladmin tune db flex slave_buffer 4096
#rladmin tune proxy all max_threads 12
#rladmin tune proxy all threads 12

apt install -y numactl


# ethtool -C $INTERFACE rx-usecs 500 tx-usecs 500 adaptive-rx off

echo "=== Redis Enterprise tuning (6 nodes / 102 primary + 102 replica / proxy threads target=8) ==="

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "ERROR: required command not found: $1"
    exit 1
  }
}

require_cmd ip
require_cmd awk
require_cmd ethtool
require_cmd numactl
require_cmd taskset
require_cmd pgrep
require_cmd grep
require_cmd sed
require_cmd wc
require_cmd tr
require_cmd sort
require_cmd head
require_cmd tail

PRIMARY_IFACE="$(ip route show default | awk '/default/ {print $5; exit}')"
if [[ -z "${PRIMARY_IFACE:-}" ]]; then
  echo "ERROR: could not determine primary network interface from default route"
  exit 1
fi

echo "Primary interface: ${PRIMARY_IFACE}"

ethtool -C $PRIMARY_IFACE rx-usecs 500 tx-usecs 500


DRIVER="$(ethtool -i "${PRIMARY_IFACE}" 2>/dev/null | awk '/driver:/ {print $2}')"
if [[ -z "${DRIVER:-}" ]]; then
  echo "ERROR: could not read NIC driver for interface ${PRIMARY_IFACE}"
  exit 1
fi

if [[ "${DRIVER}" != "gve" ]]; then
  echo "ERROR: expected gVNIC driver 'gve' on ${PRIMARY_IFACE}, found '${DRIVER}'"
  echo "Aborting because this is not the most optimized NIC configuration for GCP C4/C4A/Tier_1-style performance."
  exit 1
fi

echo "OK: interface=${PRIMARY_IFACE} driver=${DRIVER}"

DRV_VERSION="$(ethtool -i "${PRIMARY_IFACE}" 2>/dev/null | awk '/version:/ {print $2}')"
if [[ -n "${DRV_VERSION:-}" ]]; then
  echo "Driver version: ${DRV_VERSION}"
fi

NUMA_CNT="$(numactl --hardware | awk '/available:/ {print $2}')"
if [[ -z "${NUMA_CNT:-}" ]]; then
  echo "ERROR: could not determine NUMA topology"
  exit 1
fi

NUMA_ENABLED=1
NUMA0_CPUS=""
NUMA1_CPUS=""

if [[ "${NUMA_CNT}" -lt 2 ]]; then
  NUMA_ENABLED=0
  echo "WARNING: only ${NUMA_CNT} NUMA node detected; skipping NUMA-specific pinning and IRQ placement"
else
  NUMA0_CPUS="$(numactl --hardware | awk -F': ' '/node 0 cpus/ {print $2}' | sed 's/ /,/g')"
  NUMA1_CPUS="$(numactl --hardware | awk -F': ' '/node 1 cpus/ {print $2}' | sed 's/ /,/g')"

  if [[ -z "${NUMA0_CPUS:-}" || -z "${NUMA1_CPUS:-}" ]]; then
    echo "WARNING: could not read CPUs for NUMA nodes 0 and 1; skipping NUMA-specific pinning and IRQ placement"
    NUMA_ENABLED=0
  else
    echo "NUMA0 CPUs: ${NUMA0_CPUS}"
    echo "NUMA1 CPUs: ${NUMA1_CPUS}"
  fi
fi

echo "=== OS tuning ==="

echo 0 > /proc/sys/kernel/numa_balancing

if [[ -f /sys/kernel/mm/transparent_hugepage/enabled ]]; then
  echo never > /sys/kernel/mm/transparent_hugepage/enabled
fi

if [[ -f /sys/kernel/mm/transparent_hugepage/defrag ]]; then
  echo never > /sys/kernel/mm/transparent_hugepage/defrag
fi

sysctl -w net.core.somaxconn=65535 >/dev/null
sysctl -w net.core.netdev_max_backlog=250000 >/dev/null
sysctl -w net.ipv4.tcp_max_syn_backlog=65535 >/dev/null
sysctl -w net.core.rmem_max=134217728 >/dev/null
sysctl -w net.core.wmem_max=134217728 >/dev/null
sysctl -w net.ipv4.tcp_rmem="4096 87380 67108864" >/dev/null
sysctl -w net.ipv4.tcp_wmem="4096 65536 67108864" >/dev/null

echo "=== NIC tuning ==="

ethtool -G "${PRIMARY_IFACE}" rx 4096 tx 4096 || true
ethtool -K "${PRIMARY_IFACE}" rxhash on || true
ethtool -L "${PRIMARY_IFACE}" combined 12 || true

echo "=== Redis Enterprise process pinning ==="

REDIS_PIDS="$(pgrep -f 'redis-server' || true)"
if [[ -z "${REDIS_PIDS}" ]]; then
  echo "WARNING: no redis-server shard processes found"
else
  REDIS_COUNT="$(echo "${REDIS_PIDS}" | wc -l | awk '{print $1}')"
  echo "Detected redis-server shard processes: ${REDIS_COUNT}"

  if [[ "${NUMA_ENABLED}" -eq 1 ]]; then
    HALF_REDIS=$(( REDIS_COUNT / 2 ))
    echo "Pinning first ${HALF_REDIS} to NUMA0 and remaining to NUMA1"

    echo "${REDIS_PIDS}" | sort -n | head -n "${HALF_REDIS}" | while read -r pid; do
      [[ -n "${pid}" ]] && taskset -apc "${NUMA0_CPUS}" "${pid}" >/dev/null
    done

    echo "${REDIS_PIDS}" | sort -n | tail -n +"$((HALF_REDIS + 1))" | while read -r pid; do
      [[ -n "${pid}" ]] && taskset -apc "${NUMA1_CPUS}" "${pid}" >/dev/null
    done
  else
    echo "Skipping Redis shard NUMA pinning"
  fi
fi

PROXY_PIDS="$(
  {
    pgrep -f '/dmc' || true
    pgrep -f 'redis-enterprise.*proxy' || true
    pgrep -f 'proxy' || true
  } | awk 'NF' | sort -nu
)"

if [[ -z "${PROXY_PIDS}" ]]; then
  echo "WARNING: no proxy-related processes found with common patterns"
else
  PROXY_COUNT="$(echo "${PROXY_PIDS}" | wc -l | awk '{print $1}')"
  echo "Detected proxy-related processes: ${PROXY_COUNT}"

  if [[ "${NUMA_ENABLED}" -eq 1 ]]; then
    HALF_PROXY=$(( PROXY_COUNT / 2 ))
    echo "Pinning first ${HALF_PROXY} to NUMA0 and remaining to NUMA1"

    echo "${PROXY_PIDS}" | head -n "${HALF_PROXY}" | while read -r pid; do
      [[ -n "${pid}" ]] && taskset -pc "${NUMA0_CPUS}" "${pid}" >/dev/null
    done

    echo "${PROXY_PIDS}" | tail -n +"$((HALF_PROXY + 1))" | while read -r pid; do
      [[ -n "${pid}" ]] && taskset -pc "${NUMA1_CPUS}" "${pid}" >/dev/null
    done
  else
    echo "Skipping proxy NUMA pinning"
  fi
fi

echo "=== IRQ affinity ==="

IRQ_LIST="$(grep "${DRIVER}" /proc/interrupts | awk '{print $1}' | tr -d ':' || true)"
if [[ -z "${IRQ_LIST}" ]]; then
  echo "WARNING: no ${DRIVER} IRQs found in /proc/interrupts"
else
  if [[ "${NUMA_ENABLED}" -eq 1 ]]; then
    for irq in ${IRQ_LIST}; do
      echo "${NUMA1_CPUS}" > "/proc/irq/${irq}/smp_affinity_list"
    done
    echo "Pinned ${DRIVER} IRQs to NUMA1 CPUs: ${NUMA1_CPUS}"
  else
    echo "Skipping IRQ NUMA placement because only one NUMA node is available"
  fi
fi

echo "=== Verification ==="

echo "--- NIC info ---"
ethtool -i "${PRIMARY_IFACE}" || true

echo "--- Queue config ---"
ethtool -l "${PRIMARY_IFACE}" || true

echo "--- Ring sizes ---"
ethtool -g "${PRIMARY_IFACE}" || true

echo "--- NUMA balancing ---"
cat /proc/sys/kernel/numa_balancing

echo "--- THP enabled ---"
if [[ -f /sys/kernel/mm/transparent_hugepage/enabled ]]; then
  cat /sys/kernel/mm/transparent_hugepage/enabled
fi

echo "--- Redis shard affinity ---"
if [[ -n "${REDIS_PIDS:-}" ]]; then
  echo "${REDIS_PIDS}" | sort -n | while read -r pid; do
    taskset -pc "${pid}" 2>/dev/null || true
  done
fi

echo "--- Proxy affinity ---"
if [[ -n "${PROXY_PIDS:-}" ]]; then
  echo "${PROXY_PIDS}" | while read -r pid; do
    taskset -pc "${pid}" 2>/dev/null || true
  done
fi

echo "--- IRQ affinity ---"
if [[ -n "${IRQ_LIST:-}" ]]; then
  for irq in ${IRQ_LIST}; do
    printf "IRQ %s -> " "${irq}"
    cat "/proc/irq/${irq}/smp_affinity_list"
  done
fi


echo "--- Redis built-in systune.sh ---"
/opt/redislabs/sbin/systune.sh



echo "=== DONE ==="
echo "Expected topology target per node:"
echo "  - ~34 shards/node"
if [[ "${NUMA_ENABLED}" -eq 1 ]]; then
  echo "  - ~17 shard processes pinned to each NUMA node"
  echo "  - proxy target = 8 threads total; process-level pinning split across NUMA"
else
  echo "  - single NUMA node detected; NUMA-specific placement skipped"
fi
echo "Re-run this script after Redis Enterprise restarts, upgrades, resharding, or failover."