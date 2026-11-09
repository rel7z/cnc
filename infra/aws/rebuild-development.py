#!/usr/bin/env python3
"""Recreate one 8 GiB controller and three 4 GiB CNC workers within 8 vCPUs."""

import importlib.util
import json
from pathlib import Path
import sys
import time

from provision import (WORKER_TYPE, WORKERS, active_instances, aws, ensure_controller_role,
                       image, inventory, names, state_load, state_save, write_ssh)
from release import access_data, prepare

spec = importlib.util.spec_from_file_location("startup_deploy", Path(__file__).with_name("startup-deploy.py"))
startup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(startup)


def main():
    plan = inventory()
    if plan["vcpus_quota"] < 8 or plan["workers_launchable_now"] < 3:
        raise RuntimeError("Current AWS quota cannot fit the 8 GiB controller and three 4 GiB workers")
    state = state_load()
    if state.get("account") != plan["account"] or state.get("region") != plan["region"]:
        raise RuntimeError("Local AWS state does not match the selected account and region")
    if active_instances():
        raise RuntimeError("CNC instances still exist; inspect before rebuilding")
    controller_type = plan["instance_type"]
    if controller_type != "m7i-flex.large" or WORKER_TYPE != "c7i-flex.large":
        raise RuntimeError("Unexpected EC2 shape")
    archive, revision, files = prepare()
    state.setdefault("previous_deployments", []).append({
        "revision": state.get("revision"), "nodes": state.get("nodes", {}),
        "recorded_at": int(time.time()),
    })
    state.pop("revision", None)
    state.pop("verified_workers", None)
    state["nodes"] = {}
    state["instance_type"] = controller_type
    state["worker_instance_type"] = WORKER_TYPE
    state["worker_hourly_usd"] = plan["worker_hourly_usd"]
    state["ami"], state["root_device"] = image()
    state_save(state)
    ensure_controller_role(state, plan["account"], plan["subnet_id"],
                           state["worker_security_group"], state["key_name"], state["ami"])
    startup.ensure_worker_ssm_profile(state)
    url = startup.release_url(state, archive, revision)
    access = access_data()
    print(json.dumps({"release": revision[:16], "source_files": len(files),
                      "protected_tools_included": False,
                      "nodes": [controller_type, WORKER_TYPE, WORKER_TYPE, WORKER_TYPE],
                      "compute_hourly_usd": plan["development_compute_hourly_usd"]}), flush=True)
    server = "cnc-aws-server"
    server_id = startup.launch_with_user_data(
        server, state["cnc-aws-controller"], plan, state,
        startup.controller_boot(url, revision, state, access,
                                plan["controller_hourly_usd"]),
        controller=True)
    state["nodes"][server] = {"id": server_id}
    state_save(state)
    aws("ec2", "wait", "instance-running", "--instance-ids", server_id)
    server_instance = active_instances()[server]
    server_ip = server_instance["PrivateIpAddress"]
    state["nodes"][server].update(private_ip=server_ip,
                                   public_ip=server_instance.get("PublicIpAddress"))
    state_save(state)
    print(f"Controller: {server_id}", flush=True)
    for name in names(3)[1:]:
        worker_id = startup.launch_with_user_data(
            name, state["cnc-aws-workers"], plan, state,
            startup.worker_boot(name, server_ip), instance_profile="cnc-aws-worker-ssm")
        state["nodes"][name] = {"id": worker_id}
        state_save(state)
        print(f"Worker: {name} {worker_id}", flush=True)
    aws("ec2", "wait", "instance-running", "--instance-ids",
        *(state["nodes"][name]["id"] for name in names(3)))
    current = active_instances()
    for name in names(3):
        node = current[name]
        state["nodes"][name].update(private_ip=node["PrivateIpAddress"],
                                     public_ip=node.get("PublicIpAddress"))
    state_save(state)
    write_ssh(state)
    startup.wait_boot(server_id, "CNC controller bootstrap complete")
    for name in names(3)[1:]:
        startup.wait_boot(state["nodes"][name]["id"],
                          f"CNC worker {name} bootstrap complete")
    startup.verify_workers_online(state, set(names(3)[1:]))
    state.update(revision=revision, verified_workers=3, workers_target=WORKERS,
                 verified_at=int(time.time()))
    state_save(state)
    print(json.dumps({"workers_online": 3, "controller_gib": 8,
                      "worker_gib": 4}), flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyError, ValueError) as error:
        print(f"Development rebuild stopped: {error}", file=sys.stderr)
        sys.exit(1)
