#!/usr/bin/env python3
"""Finish the 8 GiB controller + 4 GiB worker build after AWS rejected t3a.medium."""

import importlib.util
import json
from pathlib import Path
import sys

from provision import (WORKER_TYPE, active_instances, aws, ensure_controller_role,
                       inventory, names, state_load, state_save, write_ssh)


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


startup = load("startup_deploy", "startup-deploy.py")
ssm = load("ssm_deploy", "ssm-deploy.py")


def main():
    state = state_load()
    plan = inventory()
    existing = active_instances()
    if set(existing) != {"cnc-aws-server"} or set(state["nodes"]) != {"cnc-aws-server"}:
        raise RuntimeError("Expected exactly the existing 8 GiB controller and no workers")
    server = state["nodes"]["cnc-aws-server"]
    if existing["cnc-aws-server"]["InstanceId"] != server["id"]:
        raise RuntimeError("Controller ID differs from saved state")
    if plan["worker_type"] != "c7i-flex.large" or plan["vcpus_quota"] < 8:
        raise RuntimeError("Compatible 4 GiB workers or quota are unavailable")
    state["worker_instance_type"] = WORKER_TYPE
    state["worker_hourly_usd"] = plan["worker_hourly_usd"]
    state_save(state)
    ensure_controller_role(state, plan["account"], plan["subnet_id"],
                           state["worker_security_group"], state["key_name"], state["ami"])
    startup.ensure_worker_ssm_profile(state)
    startup.wait_boot(server["id"], "CNC controller bootstrap complete")
    server_ip = server["private_ip"]
    for name in names(3)[1:]:
        worker_id = startup.launch_with_user_data(
            name, state["cnc-aws-workers"], plan, state,
            startup.worker_boot(name, server_ip), instance_profile="cnc-aws-worker-ssm")
        state["nodes"][name] = {"id": worker_id}
        state_save(state)
        print(f"4 GiB worker {name}: {worker_id}", flush=True)
    aws("ec2", "wait", "instance-running", "--instance-ids",
        *(state["nodes"][name]["id"] for name in names(3)[1:]))
    current = active_instances()
    for name in names(3)[1:]:
        item = current[name]
        state["nodes"][name].update(private_ip=item["PrivateIpAddress"],
                                     public_ip=item.get("PublicIpAddress"))
        state_save(state)
        startup.wait_boot(state["nodes"][name]["id"],
                          f"CNC worker {name} bootstrap complete")
    write_ssh(state)
    startup.verify_workers_online(state, set(names(3)[1:]))
    ssm.main()
    print(json.dumps({"controller_gib": 8, "worker_gib": 4,
                      "workers_online": 3,
                      "compute_hourly_usd": plan["development_compute_hourly_usd"]}), flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyError, ValueError) as error:
        print(f"Development resume stopped: {error}", file=sys.stderr)
        sys.exit(1)
