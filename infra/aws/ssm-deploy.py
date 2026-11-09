#!/usr/bin/env python3
"""Deploy the unrestricted CNC release through SSM when direct SSH is unavailable."""

import base64
import hashlib
import json
from pathlib import Path
import secrets
import subprocess
import sys
import time

from provision import REGION, PROFILE, STATE_FILE, state_save, aws
from release import prepare, access_data, encoded, hourly_price, CLUSTER, TYPE
from provision import WORKER_TYPE


def ensure_workers_managed(state):
    role = "cnc-aws-worker-ssm"
    roles = aws("iam", "list-roles", region="us-east-1")["Roles"]
    if not any(item["RoleName"] == role for item in roles):
        trust = {"Version": "2012-10-17", "Statement": [{"Effect": "Allow",
                 "Principal": {"Service": "ec2.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
        aws("iam", "create-role", "--role-name", role,
            "--assume-role-policy-document", json.dumps(trust),
            "--tags", f"Key=CNCCluster,Value={CLUSTER}", region="us-east-1")
    aws("iam", "attach-role-policy", "--role-name", role,
        "--policy-arn", "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore", region="us-east-1")
    profiles = aws("iam", "list-instance-profiles", region="us-east-1")["InstanceProfiles"]
    profile = next((item for item in profiles if item["InstanceProfileName"] == role), None)
    if not profile:
        aws("iam", "create-instance-profile", "--instance-profile-name", role,
            "--tags", f"Key=CNCCluster,Value={CLUSTER}", region="us-east-1")
        aws("iam", "add-role-to-instance-profile", "--instance-profile-name", role,
            "--role-name", role, region="us-east-1")
    elif not any(item["RoleName"] == role for item in profile["Roles"]):
        raise RuntimeError("Existing worker SSM profile has an unexpected role")
    for name, node in state["nodes"].items():
        if name.endswith("-server"):
            continue
        associations = aws("ec2", "describe-iam-instance-profile-associations", "--filters",
                           f"Name=instance-id,Values={node['id']}")["IamInstanceProfileAssociations"]
        if not associations:
            aws("ec2", "associate-iam-instance-profile", "--instance-id", node["id"],
                "--iam-instance-profile", "Name=" + role)
        elif not any(item["IamInstanceProfile"]["Arn"].endswith("/" + role)
                     for item in associations):
            raise RuntimeError(f"Unexpected instance profile on {name}")


def wait_managed(state):
    wanted = {node["id"] for node in state["nodes"].values()}
    deadline = time.monotonic() + 600
    while time.monotonic() < deadline:
        response = aws("ssm", "describe-instance-information", "--filters",
                       "Key=InstanceIds,Values=" + ",".join(sorted(wanted)))
        online = {item["InstanceId"] for item in response["InstanceInformationList"]
                  if item["PingStatus"] == "Online"}
        if wanted <= online:
            return
        print(f"SSM online: {len(online)}/{len(wanted)}", flush=True)
        time.sleep(15)
    raise RuntimeError("Timed out waiting for EC2 nodes to register with SSM")


def release_bucket(state, archive, revision):
    name = state.get("release_bucket") or f"cnc-release-{state['account']}-{secrets.token_hex(5)}"
    if not state.get("release_bucket"):
        aws("s3api", "create-bucket", "--bucket", name, "--create-bucket-configuration",
            "LocationConstraint=" + REGION)
        aws("s3api", "put-public-access-block", "--bucket", name, "--public-access-block-configuration",
            "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true")
        aws("s3api", "put-bucket-encryption", "--bucket", name,
            "--server-side-encryption-configuration",
            '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}')
        state["release_bucket"] = name
        state_save(state)
    key = f"releases/{revision}.tar.gz"
    aws("s3api", "put-object", "--bucket", name, "--key", key,
        "--body", str(archive), "--server-side-encryption", "AES256")
    result = subprocess.run(["aws", "s3", "presign", f"s3://{name}/{key}",
                             "--expires-in", "3600", "--region", REGION,
                             "--profile", PROFILE], capture_output=True, text=True, check=True)
    return result.stdout.strip()


def send(instance, script, label):
    params = json.dumps({"commands": [script], "executionTimeout": ["7200"]})
    result = aws("ssm", "send-command", "--instance-ids", instance,
                 "--document-name", "AWS-RunShellScript", "--comment", label,
                 "--parameters", params)["Command"]
    command_id = result["CommandId"]
    print(f"{label}: {command_id}", flush=True)
    deadline = time.monotonic() + 7500
    while time.monotonic() < deadline:
        try:
            invocation = aws("ssm", "get-command-invocation", "--command-id", command_id,
                             "--instance-id", instance)
        except RuntimeError as error:
            if "InvocationDoesNotExist" in str(error):
                time.sleep(5)
                continue
            raise
        status = invocation["Status"]
        if status == "Success":
            print(f"{label}: success", flush=True)
            return
        if status in ("Failed", "Cancelled", "TimedOut", "Cancelling"):
            raise RuntimeError(f"{label}: {status}: {invocation.get('StandardErrorContent', '')[-2000:]}")
        time.sleep(10)
    raise RuntimeError(f"{label}: timed out")


def main():
    state = json.loads(STATE_FILE.read_text())
    archive, revision, files = prepare()
    print(f"Release {revision[:16]}: {len(files)} unrestricted files", flush=True)
    ensure_workers_managed(state)
    wait_managed(state)
    url = release_bucket(state, archive, revision)
    server = state["nodes"]["cnc-aws-server"]
    access = access_data()
    cloud = {"region": REGION, "cluster": CLUSTER, "instance_type": TYPE,
             "worker_instance_type": WORKER_TYPE,
             "controller_private_ip": server["private_ip"], "subnet_id": state["subnet_id"],
             "worker_security_group": state["worker_security_group"],
             "key_name": state["key_name"], "ami": state["ami"],
             "root_device": state["root_device"], "hourly_price_usd": hourly_price(),
             "worker_hourly_price_usd": hourly_price(WORKER_TYPE)}
    payload = {"private_ip": server["private_ip"], "revision": revision,
               "login_user": access["username"], "login_password": access["password"],
               "session_secret": access["session_secret"], "aws_config": cloud}
    script = ("set -e\n"
              f"curl -fsSL --max-time 300 '{url}' -o /tmp/cnc-source.tar.gz\n"
              f"echo '{revision}  /tmp/cnc-source.tar.gz' | sha256sum -c -\n"
              f"mkdir -p /opt/cnc/releases/{revision[:16]}\n"
              f"tar -xzf /tmp/cnc-source.tar.gz -C /opt/cnc/releases/{revision[:16]}\n"
              f"ln -sfn /opt/cnc/releases/{revision[:16]} /opt/cnc/source\n"
              f"export CNC_DEPLOY_DATA={encoded(payload)}\n"
              "bash /opt/cnc/source/infra/aws/install-controller.sh\n")
    send(server["id"], script, "cnc-controller-deploy")
    installer = (Path(__file__).resolve().parent / "install-worker.sh").read_text()
    for name, node in state["nodes"].items():
        if name.endswith("-server"):
            continue
        worker_payload = {"name": name, "private_ip": server["private_ip"]}
        worker_script = f"export CNC_DEPLOY_DATA={encoded(worker_payload)}\n" + installer
        send(node["id"], "bash <<'CNC_WORKER_INSTALL'\n" + worker_script + "\nCNC_WORKER_INSTALL\n",
             f"cnc-deploy-{name}")
    for name, node in state["nodes"].items():
        service = "cnc-server cnc-processor cnc-ui" if name.endswith("-server") else "cnc-worker"
        send(node["id"], f'test "$(cat /opt/cnc/RELEASE)" = "{revision}" && '
             f'test "$(systemctl is-active {service} | grep -c active)" -eq {len(service.split())}',
             f"cnc-verify-{name}")
    state.update(revision=revision, verified_workers=len(state["nodes"]) - 1,
                 verified_at=int(time.time()))
    state_save(state)
    print(json.dumps({"revision": revision, "workers_deployed": len(state["nodes"]) - 1}), flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyError, ValueError, subprocess.CalledProcessError) as error:
        print(f"SSM deployment stopped: {error}", file=sys.stderr)
        sys.exit(1)
