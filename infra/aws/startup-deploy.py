#!/usr/bin/env python3
"""Replace empty EC2 nodes with first-boot CNC installers; no inbound SSH required."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import secrets
import subprocess
import sys
import time

from provision import (CLUSTER, REGION, PROFILE, STATE_FILE, TYPE, WORKER_TYPE, WORKERS, active_instances,
                       aws, inventory, launch, names, state_load, state_save, write_ssh)
from release import access_data, prepare


def release_url(state, archive, revision):
    bucket = state.get("release_bucket") or f"cnc-release-{state['account']}-{secrets.token_hex(5)}"
    if not state.get("release_bucket"):
        aws("s3api", "create-bucket", "--bucket", bucket,
            "--create-bucket-configuration", "LocationConstraint=" + REGION)
        state["release_bucket"] = bucket
        state_save(state)
    aws("s3api", "put-public-access-block", "--bucket", bucket,
        "--public-access-block-configuration",
        "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true")
    aws("s3api", "put-bucket-encryption", "--bucket", bucket,
        "--server-side-encryption-configuration",
        '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}')
    key = f"releases/{revision}.tar.gz"
    aws("s3api", "put-object", "--bucket", bucket, "--key", key,
        "--body", str(archive), "--server-side-encryption", "AES256")
    result = subprocess.run(["aws", "s3", "presign", f"s3://{bucket}/{key}",
                             "--expires-in", "3600", "--region", REGION,
                             "--profile", PROFILE], check=True, capture_output=True, text=True)
    return result.stdout.strip()


def encode(data):
    return base64.b64encode(json.dumps(data, separators=(",", ":")).encode()).decode()


def ensure_worker_ssm_profile(state):
    role = "cnc-aws-worker-ssm"
    roles = aws("iam", "list-roles", region="us-east-1")["Roles"]
    if not any(item["RoleName"] == role for item in roles):
        trust = {"Version": "2012-10-17", "Statement": [{"Effect": "Allow",
                 "Principal": {"Service": "ec2.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
        aws("iam", "create-role", "--role-name", role,
            "--assume-role-policy-document", json.dumps(trust),
            "--tags", f"Key=CNCCluster,Value={CLUSTER}", region="us-east-1")
    aws("iam", "attach-role-policy", "--role-name", role,
        "--policy-arn", "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
        region="us-east-1")
    profiles = aws("iam", "list-instance-profiles", region="us-east-1")["InstanceProfiles"]
    profile = next((item for item in profiles if item["InstanceProfileName"] == role), None)
    if not profile:
        aws("iam", "create-instance-profile", "--instance-profile-name", role,
            "--tags", f"Key=CNCCluster,Value={CLUSTER}", region="us-east-1")
        aws("iam", "add-role-to-instance-profile", "--instance-profile-name", role,
            "--role-name", role, region="us-east-1")
    elif not any(item["RoleName"] == role for item in profile["Roles"]):
        raise RuntimeError("Worker SSM profile has an unexpected role")


def controller_boot(url, revision, state, access, hourly):
    config = {"region": REGION, "cluster": CLUSTER, "instance_type": TYPE,
              "controller_private_ip": "AUTO", "subnet_id": state["subnet_id"],
              "worker_security_group": state["worker_security_group"],
              "key_name": state["key_name"], "ami": state["ami"],
              "root_device": state["root_device"], "hourly_price_usd": hourly,
              "worker_instance_type": WORKER_TYPE, "worker_hourly_price_usd": state["worker_hourly_usd"]}
    payload = {"private_ip": "AUTO", "revision": revision,
               "login_user": access["username"], "login_password": access["password"],
               "session_secret": access["session_secret"], "aws_config": config}
    return ("#!/bin/bash\nset -euo pipefail\n"
            "exec > >(tee -a /var/log/cnc-bootstrap.log /dev/console) 2>&1\n"
            "echo 'CNC controller bootstrap starting'\n"
            f"curl -fsSL --retry 5 --max-time 300 '{url}' -o /tmp/cnc-source.tar.gz\n"
            f"echo '{revision}  /tmp/cnc-source.tar.gz' | sha256sum -c -\n"
            f"mkdir -p /opt/cnc/releases/{revision[:16]}\n"
            f"tar -xzf /tmp/cnc-source.tar.gz -C /opt/cnc/releases/{revision[:16]}\n"
            f"ln -sfn /opt/cnc/releases/{revision[:16]} /opt/cnc/source\n"
            f"export CNC_DEPLOY_DATA={encode(payload)}\n"
            "export CNC_DEPLOY_DATA=$(python3 - <<'PY'\n"
            "import base64,json,os,subprocess\n"
            "data=json.loads(base64.b64decode(os.environ['CNC_DEPLOY_DATA']))\n"
            "ip=subprocess.check_output(['hostname','-I'],text=True).split()[0]\n"
            "data['private_ip']=ip\n"
            "data['aws_config']['controller_private_ip']=ip\n"
            "print(base64.b64encode(json.dumps(data).encode()).decode())\n"
            "PY\n)\n"
            "bash /opt/cnc/source/infra/aws/install-controller.sh\n"
            "echo 'CNC controller bootstrap complete'\n")


def worker_boot(name, server_ip):
    installer = (Path(__file__).resolve().parent / "install-worker.sh").read_text()
    payload = encode({"name": name, "private_ip": server_ip})
    return ("#!/bin/bash\nset -euo pipefail\n"
            "exec > >(tee -a /var/log/cnc-bootstrap.log /dev/console) 2>&1\n"
            f"echo 'CNC worker {name} bootstrap starting'\n"
            f"export CNC_DEPLOY_DATA={payload}\n"
            + installer + f"\necho 'CNC worker {name} bootstrap complete'\n")


def launch_with_user_data(name, group, plan, state, script, controller=False, private_ip=None,
                          instance_profile=None):
    token = secrets.token_hex(16)
    state.setdefault("tokens", {})[name] = token
    state_save(state)
    mappings = [{"DeviceName": state["root_device"], "Ebs": {"VolumeSize": 40,
                 "VolumeType": "gp3", "DeleteOnTermination": True, "Encrypted": True}}]
    tags = f"ResourceType=instance,Tags=[{{Key=Name,Value={name}}},{{Key=CNCCluster,Value={CLUSTER}}}]"
    args = ["ec2", "run-instances", "--image-id", state["ami"],
            "--instance-type", TYPE if controller else WORKER_TYPE,
            "--count", "1", "--key-name", state["key_name"], "--subnet-id", plan["subnet_id"],
            "--security-group-ids", group, "--associate-public-ip-address",
            "--block-device-mappings", json.dumps(mappings), "--metadata-options",
            "HttpEndpoint=enabled,HttpTokens=required", "--tag-specifications", tags,
            "--client-token", token, "--user-data", script]
    if controller:
        args.extend(["--iam-instance-profile", "Name=cnc-aws-controller"])
    if instance_profile:
        args.extend(["--iam-instance-profile", "Name=" + instance_profile])
    if private_ip:
        args.extend(["--private-ip-address", private_ip])
    response = aws(*args)
    return response["Instances"][0]["InstanceId"]


def console_text(instance_id):
    return aws("ec2", "get-console-output", "--instance-id", instance_id,
               "--latest").get("Output") or ""


def wait_boot(instance_id, marker, timeout=2400):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        output = console_text(instance_id)
        if marker in output:
            return
        if "CNC controller bootstrap starting" in output or "CNC worker" in output:
            print(f"Waiting for {marker}", flush=True)
        time.sleep(30)
    raise RuntimeError(f"Timed out waiting for {marker}; inspect EC2 console for {instance_id}")


def verify_workers_online(state, expected):
    server = state["nodes"]["cnc-aws-server"]
    address = server["private_ip"]
    deadline = time.monotonic() + 300
    while time.monotonic() < deadline:
        command = aws("ssm", "send-command", "--instance-ids", server["id"],
                      "--document-name", "AWS-RunShellScript", "--parameters",
                      json.dumps({"commands": [f"curl -fsS http://{address}:8080/api/workers"]}))
        command_id = command["Command"]["CommandId"]
        time.sleep(3)
        result = aws("ssm", "get-command-invocation", "--command-id", command_id,
                     "--instance-id", server["id"])
        if result["Status"] == "Success":
            workers = json.loads(result["StandardOutputContent"])
            online = {item["id"] for item in workers if item.get("status") == "online"}
            if expected <= online:
                return
        time.sleep(10)
    raise RuntimeError("Workers booted but did not all register with the CNC controller")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--replace-empty-nodes", action="store_true",
                        help="Terminate the four failed, empty nodes and launch first-boot replacements")
    parser.add_argument("--retry-controller", action="store_true",
                        help="Replace only an empty failed controller, preserving its private IP")
    parser.add_argument("--add-workers", action="store_true",
                        help="Add workers 4-7 after AWS approves a 16-vCPU quota")
    parser.add_argument("--accept-compute-hourly-usd", type=float,
                        help="Required for --add-workers; must cover the four new nodes' hourly compute")
    args = parser.parse_args()
    if sum((args.replace_empty_nodes, args.retry_controller, args.add_workers)) != 1:
        raise RuntimeError("Choose exactly one replacement operation")
    state = state_load()
    plan = inventory()
    if args.add_workers:
        if plan["vcpus_quota"] < 16 or plan["workers_launchable_now"] < WORKERS:
            raise RuntimeError("AWS has not approved the 16-vCPU quota needed for seven 8 GiB workers")
        if not state.get("revision") or set(state["nodes"]) != set(names(3)):
            raise RuntimeError("Expected a verified controller and workers 1-3")
        extra_hourly = 4 * plan["worker_hourly_usd"]
        if args.accept_compute_hourly_usd is None or args.accept_compute_hourly_usd < extra_hourly - 0.000001:
            raise RuntimeError(f"Acknowledge the four new workers' ${extra_hourly:.5f}/hour compute rate")
        existing = active_instances()
        if set(existing) != set(names(3)):
            raise RuntimeError("Unexpected CNC instance state; inspect before adding workers")
        server_ip = state["nodes"]["cnc-aws-server"]["private_ip"]
        for name in names(WORKERS)[4:]:
            instance_id = launch_with_user_data(name, state["cnc-aws-workers"], plan, state,
                                                worker_boot(name, server_ip),
                                                instance_profile="cnc-aws-worker-ssm")
            state["nodes"][name] = {"id": instance_id}
            state_save(state)
            print(f"Added {name}: {instance_id}", flush=True)
        aws("ec2", "wait", "instance-running", "--instance-ids",
            *(state["nodes"][name]["id"] for name in names(WORKERS)[4:]))
        current = active_instances()
        for name in names(WORKERS)[4:]:
            item = current[name]
            state["nodes"][name].update(private_ip=item["PrivateIpAddress"],
                                         public_ip=item["PublicIpAddress"])
            state_save(state)
            wait_boot(state["nodes"][name]["id"], f"CNC worker {name} bootstrap complete")
        write_ssh(state)
        verify_workers_online(state, set(names(WORKERS)[1:]))
        state.update(verified_workers=WORKERS, workers_target=WORKERS, verified_at=int(time.time()))
        state_save(state)
        print(json.dumps({"workers_online": WORKERS}), flush=True)
        return
    if plan["account"] != state.get("account") or plan["workers_launchable_now"] < 3:
        raise RuntimeError("AWS account or quota differs from the planned four-node deployment")
    if state.get("revision"):
        raise RuntimeError("Refusing to replace nodes with a verified CNC release")
    archive, revision, files = prepare()
    print(f"Verified unrestricted release {revision[:16]} ({len(files)} files)", flush=True)
    url = release_url(state, archive, revision)
    access = access_data()
    if args.retry_controller:
        controller = "cnc-aws-server"
        old = state["nodes"][controller]
        private_ip = old["private_ip"]
        print(f"Terminating failed controller: {old['id']}", flush=True)
        aws("ec2", "terminate-instances", "--instance-ids", old["id"])
        aws("ec2", "wait", "instance-terminated", "--instance-ids", old["id"])
        hourly = plan["compute_hourly_usd"] / 8
        server_id = launch_with_user_data(controller, state["cnc-aws-controller"], plan, state,
                                          controller_boot(url, revision, state, access, hourly),
                                          True, private_ip)
        state["nodes"][controller] = {"id": server_id, "private_ip": private_ip}
        state_save(state)
        aws("ec2", "wait", "instance-running", "--instance-ids", server_id)
        state["nodes"][controller]["public_ip"] = active_instances()[controller]["PublicIpAddress"]
        state_save(state)
        write_ssh(state)
        print(f"Controller retry: {server_id}", flush=True)
        wait_boot(server_id, "CNC controller bootstrap complete")
        for name in names(3)[1:]:
            wait_boot(state["nodes"][name]["id"], f"CNC worker {name} bootstrap complete")
        state.update(revision=revision, verified_workers=3, workers_target=WORKERS,
                     verified_at=int(time.time()))
        state_save(state)
        print(json.dumps({"release": revision, "workers_installed": 3}), flush=True)
        return
    existing = active_instances()
    for name in names(3):
        if name in existing:
            print(f"Terminating empty {name}: {existing[name]['InstanceId']}", flush=True)
            aws("ec2", "terminate-instances", "--instance-ids", existing[name]["InstanceId"])
    if existing:
        aws("ec2", "wait", "instance-terminated", "--instance-ids",
            *(item["InstanceId"] for name, item in existing.items() if name in names(3)))
    state["nodes"] = {}
    state_save(state)
    hourly = plan["controller_hourly_usd"]
    controller = names(3)[0]
    server_id = launch_with_user_data(controller, state["cnc-aws-controller"], plan, state,
                                      controller_boot(url, revision, state, access, hourly), True)
    state["nodes"][controller] = {"id": server_id}
    state_save(state)
    aws("ec2", "wait", "instance-running", "--instance-ids", server_id)
    item = active_instances()[controller]
    server_ip = item["PrivateIpAddress"]
    state["nodes"][controller].update(private_ip=server_ip, public_ip=item["PublicIpAddress"])
    state_save(state)
    print(f"Controller replacement: {server_id}", flush=True)
    for name in names(3)[1:]:
        instance_id = launch_with_user_data(name, state["cnc-aws-workers"], plan, state,
                                            worker_boot(name, server_ip))
        state["nodes"][name] = {"id": instance_id}
        state_save(state)
        print(f"Worker replacement {name}: {instance_id}", flush=True)
    aws("ec2", "wait", "instance-running", "--instance-ids",
        *(state["nodes"][name]["id"] for name in names(3)))
    current = active_instances()
    for name in names(3):
        item = current[name]
        state["nodes"][name].update(private_ip=item["PrivateIpAddress"],
                                     public_ip=item["PublicIpAddress"])
    state_save(state)
    write_ssh(state)
    wait_boot(server_id, "CNC controller bootstrap complete")
    for name in names(3)[1:]:
        wait_boot(state["nodes"][name]["id"], f"CNC worker {name} bootstrap complete")
    state.update(revision=revision, verified_workers=3, workers_target=WORKERS,
                 verified_at=int(time.time()))
    state_save(state)
    print(json.dumps({"release": revision, "workers_installed": 3}), flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Startup deployment stopped: {error}", file=sys.stderr)
        sys.exit(1)
