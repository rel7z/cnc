#!/usr/bin/env python3
"""Plan or provision the CNC EC2 controller and seven independent workers.

This script uses the local ``cnc`` AWS CLI profile. It never reads CNC tool
contents. Run without --apply first; instance creation is gated on quota and an
explicit acknowledgement of the current hourly EC2 compute price.
"""

import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from ssh_identity import deployment_key, identity_lines, key_name as shared_key_name
from ssh_policy import key_only_setup


ROOT = Path(__file__).resolve().parents[2]
STATE_DIR = ROOT / ".aws-cnc"
STATE_FILE = STATE_DIR / "state.json"
REGION = "us-east-2"
PROFILE = "cnc"
CLUSTER = "cnc-aws"
TYPE = "m7i-flex.large"
WORKER_TYPE = "c7i-flex.large"
WORKERS = 7
QUOTA_CODE = "L-1216C47A"
AMI_PARAMETER = "/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id"


def aws(*args, region=REGION):
    command = ["aws", *args, "--region", region, "--profile", PROFILE, "--output", "json"]
    result = subprocess.run(command, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or f"AWS command failed: {args[:2]}")
    return json.loads(result.stdout) if result.stdout.strip() else {}


def state_save(state):
    STATE_DIR.mkdir(mode=0o700, exist_ok=True)
    STATE_DIR.chmod(0o700)
    temporary = STATE_FILE.with_suffix(".tmp")
    temporary.write_text(json.dumps(state, indent=2, sort_keys=True) + "\n")
    temporary.chmod(0o600)
    temporary.replace(STATE_FILE)


def state_load():
    return json.loads(STATE_FILE.read_text()) if STATE_FILE.exists() else {}


def names(workers=WORKERS):
    return ["cnc-aws-server", *(f"cnc-aws-worker-{number}" for number in range(1, workers + 1))]


def active_instances():
    response = aws("ec2", "describe-instances", "--filters",
                   f"Name=tag:CNCCluster,Values={CLUSTER}",
                   "Name=instance-state-name,Values=pending,running,stopping,stopped")
    instances = [item for reservation in response["Reservations"] for item in reservation["Instances"]]
    by_name = {}
    for item in instances:
        tags = {tag["Key"]: tag["Value"] for tag in item.get("Tags", [])}
        name = tags.get("Name", "")
        if name in by_name:
            raise RuntimeError(f"Duplicate CNC instance name in AWS: {name}")
        by_name[name] = item
    return by_name


def hourly_price(instance_type=TYPE):
    response = aws("pricing", "get-products", "--service-code", "AmazonEC2",
                   "--filters", f"Type=TERM_MATCH,Field=instanceType,Value={instance_type}",
                   "Type=TERM_MATCH,Field=location,Value=US East (Ohio)",
                   "Type=TERM_MATCH,Field=operatingSystem,Value=Linux",
                   "Type=TERM_MATCH,Field=tenancy,Value=Shared",
                   "Type=TERM_MATCH,Field=preInstalledSw,Value=NA",
                   "--max-results", "100", region="us-east-1")
    for raw in response["PriceList"]:
        offer = json.loads(raw)
        if offer["product"]["attributes"].get("usagetype") != f"USE2-BoxUsage:{instance_type}":
            continue
        for term in offer["terms"].get("OnDemand", {}).values():
            for dimension in term["priceDimensions"].values():
                if dimension["unit"] == "Hrs":
                    return float(dimension["pricePerUnit"]["USD"])
    raise RuntimeError("Could not verify current Ohio on-demand compute price")


def inventory():
    identity = aws("sts", "get-caller-identity")
    plan = aws("freetier", "get-account-plan-state", region="us-east-1")
    quota = aws("service-quotas", "get-service-quota", "--service-code", "ec2",
                "--quota-code", QUOTA_CODE)["Quota"]
    kind = aws("ec2", "describe-instance-types", "--instance-types", TYPE)["InstanceTypes"][0]
    if kind["MemoryInfo"]["SizeInMiB"] != 8192 or "x86_64" not in kind["ProcessorInfo"]["SupportedArchitectures"]:
        raise RuntimeError(f"{TYPE} no longer matches the required 8 GiB x86_64 shape")
    worker_kind = aws("ec2", "describe-instance-types", "--instance-types", WORKER_TYPE)["InstanceTypes"][0]
    if (worker_kind["MemoryInfo"]["SizeInMiB"] != 4096
            or "x86_64" not in worker_kind["ProcessorInfo"]["SupportedArchitectures"]
            or worker_kind["VCpuInfo"]["DefaultVCpus"] != kind["VCpuInfo"]["DefaultVCpus"]):
        raise RuntimeError(f"{WORKER_TYPE} no longer matches the required 4 GiB x86_64 worker shape")
    vpcs = aws("ec2", "describe-vpcs", "--filters", "Name=isDefault,Values=true")["Vpcs"]
    if len(vpcs) != 1:
        raise RuntimeError("Expected exactly one default VPC in us-east-2")
    vpc = vpcs[0]
    subnets = aws("ec2", "describe-subnets", "--filters", f"Name=vpc-id,Values={vpc['VpcId']}")["Subnets"]
    candidates = [item for item in subnets if item.get("MapPublicIpOnLaunch") and item["AvailableIpAddressCount"] >= 16]
    if not candidates:
        raise RuntimeError("No default public subnet with enough available addresses")
    subnet = sorted(candidates, key=lambda item: item["AvailabilityZone"])[0]
    current = active_instances()
    unexpected = set(current) - set(names(WORKERS))
    if unexpected:
        raise RuntimeError(f"Unexpected instances have the {CLUSTER} tag: {sorted(unexpected)}")
    hourly = hourly_price()
    worker_hourly = hourly_price(WORKER_TYPE)
    per_instance_vcpus = kind["VCpuInfo"]["DefaultVCpus"]
    workers_now = max(0, min(WORKERS, int(quota["Value"] // per_instance_vcpus) - 1))
    return {
        "account": identity["Account"], "account_plan": plan["accountPlanType"],
        "region": REGION, "vpc_id": vpc["VpcId"], "subnet_id": subnet["SubnetId"],
        "availability_zone": subnet["AvailabilityZone"], "instance_type": TYPE,
        "instance_memory_gib": 8, "instances_requested": len(names()),
        "worker_type": WORKER_TYPE, "worker_memory_gib": 4,
        "instances_launchable_now": len(names(workers_now)),
        "workers_launchable_now": workers_now,
        "vcpus_per_instance": per_instance_vcpus,
        "vcpus_required": len(names()) * per_instance_vcpus,
        "vcpus_quota": quota["Value"],
        "free_tier_eligible_type": kind.get("FreeTierEligible", False),
        "worker_free_tier_eligible_type": worker_kind.get("FreeTierEligible", False),
        "controller_hourly_usd": hourly,
        "compute_hourly_usd": round(hourly + WORKERS * worker_hourly, 5),
        "development_compute_hourly_usd": round(hourly + workers_now * worker_hourly, 5),
        "target_compute_hourly_usd": round(hourly + WORKERS * worker_hourly, 5),
        "worker_hourly_usd": worker_hourly,
        "compute_730h_usd": round((hourly + WORKERS * worker_hourly) * 730, 2),
        "current_quota_compute_hourly_usd": round(hourly + workers_now * worker_hourly, 5),
        "extra_charges": "EBS, public IPv4, transfer, and any applicable CPU overage are additional",
        "existing": {name: item["InstanceId"] for name, item in current.items()},
    }


def ensure_key(state):
    path = deployment_key(ROOT, STATE_DIR)
    path.chmod(0o600)
    key_name = shared_key_name(path)
    found = aws("ec2", "describe-key-pairs", "--include-public-key", "--filters", f"Name=key-name,Values={key_name}")["KeyPairs"]
    if not found:
        aws("ec2", "import-key-pair", "--key-name", key_name,
            "--public-key-material", "fileb://" + str(path.with_suffix(".pub")),
            "--tag-specifications", f"ResourceType=key-pair,Tags=[{{Key=CNCCluster,Value={CLUSTER}}}]")
    elif found[0].get('PublicKey', '').split()[:2] != path.with_suffix('.pub').read_text().split()[:2]:
        raise RuntimeError("Existing EC2 shared key does not match this Mac")
    if state.get('key_name') and state['key_name'] != key_name:
        state.setdefault('legacy_key_names', []).append(state['key_name'])
    state["key_name"] = key_name
    state_save(state)
    return key_name, path


def ensure_group(state, name, description, vpc_id):
    groups = aws("ec2", "describe-security-groups", "--filters",
                 f"Name=group-name,Values={name}", f"Name=vpc-id,Values={vpc_id}")["SecurityGroups"]
    if len(groups) > 1:
        raise RuntimeError(f"Duplicate security groups named {name}")
    if groups:
        group_id = groups[0]["GroupId"]
        if state.get(name) != group_id:
            raise RuntimeError(f"Security group {name} exists but is not recorded as CNC-owned")
    else:
        group_id = aws("ec2", "create-security-group", "--group-name", name,
                       "--description", description, "--vpc-id", vpc_id,
                       "--tag-specifications", f"ResourceType=security-group,Tags=[{{Key=CNCCluster,Value={CLUSTER}}}]")["GroupId"]
        state[name] = group_id
        state_save(state)
    return group_id


def allow_ingress(group_id, protocol, port, source):
    permission = {"IpProtocol": protocol, "FromPort": port, "ToPort": port}
    if source.startswith("sg-"):
        permission["UserIdGroupPairs"] = [{"GroupId": source}]
    else:
        permission["IpRanges"] = [{"CidrIp": source}]
    result = subprocess.run(["aws", "ec2", "authorize-security-group-ingress", "--group-id", group_id,
                             "--ip-permissions", json.dumps([permission]), "--region", REGION,
                             "--profile", PROFILE, "--output", "json"], capture_output=True, text=True)
    if result.returncode and "InvalidPermission.Duplicate" not in result.stderr:
        raise RuntimeError(result.stderr.strip())


def image():
    value = aws("ssm", "get-parameter", "--name", AMI_PARAMETER)["Parameter"]["Value"]
    details = aws("ec2", "describe-images", "--image-ids", value)["Images"]
    if len(details) != 1 or details[0]["Architecture"] != "x86_64" or details[0]["State"] != "available":
        raise RuntimeError("Ubuntu 24.04 AMI is unavailable or incompatible")
    return value, details[0]["RootDeviceName"]


def ensure_controller_role(state, account, subnet_id, worker_group, key_name, ami):
    role_name = "cnc-aws-controller"
    roles = aws("iam", "list-roles", region="us-east-1")["Roles"]
    existing = next((role for role in roles if role["RoleName"] == role_name), None)
    if existing and state.get("controller_role") != role_name:
        raise RuntimeError("Controller IAM role already exists but is not recorded as CNC-owned")
    if not existing:
        trust = {"Version": "2012-10-17", "Statement": [{"Effect": "Allow",
                 "Principal": {"Service": "ec2.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
        aws("iam", "create-role", "--role-name", role_name,
            "--assume-role-policy-document", json.dumps(trust),
            "--tags", f"Key=CNCCluster,Value={CLUSTER}", region="us-east-1")
        state["controller_role"] = role_name
        state_save(state)
    prefix = f"arn:aws:ec2:{REGION}:{account}:"
    policy = {"Version": "2012-10-17", "Statement": [
        {"Effect": "Allow", "Action": ["ec2:DescribeInstances", "ec2:DescribeInstanceTypes",
         "ec2:DescribeImages", "ec2:DescribeSubnets", "ec2:DescribeSecurityGroups",
         "ec2:DescribeVpcs", "servicequotas:GetServiceQuota"], "Resource": "*"},
        {"Effect": "Allow", "Action": "ec2:RunInstances", "Resource": prefix + "instance/*",
         "Condition": {"StringEquals": {"ec2:InstanceType": WORKER_TYPE}}},
        {"Effect": "Allow", "Action": "ec2:RunInstances", "Resource": [
            f"arn:aws:ec2:{REGION}::image/{ami}", prefix + f"subnet/{subnet_id}",
            prefix + f"security-group/{worker_group}", prefix + f"key-pair/{key_name}",
            prefix + "network-interface/*", prefix + "volume/*"]},
        {"Effect": "Allow", "Action": "ec2:CreateTags", "Resource": prefix + "instance/*",
         "Condition": {"StringEquals": {"ec2:CreateAction": "RunInstances"}}},
        {"Effect": "Allow", "Action": "iam:PassRole",
         "Resource": f"arn:aws:iam::{account}:role/cnc-aws-worker-ssm",
         "Condition": {"StringEquals": {"iam:PassedToService": "ec2.amazonaws.com"}}},
    ]}
    aws("iam", "put-role-policy", "--role-name", role_name, "--policy-name", "cnc-worker-launch",
        "--policy-document", json.dumps(policy), region="us-east-1")
    profiles = aws("iam", "list-instance-profiles", region="us-east-1")["InstanceProfiles"]
    profile = next((item for item in profiles if item["InstanceProfileName"] == role_name), None)
    if not profile:
        aws("iam", "create-instance-profile", "--instance-profile-name", role_name,
            "--tags", f"Key=CNCCluster,Value={CLUSTER}", region="us-east-1")
        aws("iam", "add-role-to-instance-profile", "--instance-profile-name", role_name,
            "--role-name", role_name, region="us-east-1")
    elif not any(role["RoleName"] == role_name for role in profile["Roles"]):
        raise RuntimeError("Controller instance profile has an unexpected role")
    state["controller_role"] = role_name
    state_save(state)
    return role_name


def launch(name, group_id, plan, key_name, ami, root_device, token, volume_gib, controller_profile=None):
    mappings = [{"DeviceName": root_device, "Ebs": {"VolumeSize": volume_gib,
                 "VolumeType": "gp3", "DeleteOnTermination": True, "Encrypted": True}}]
    tag = f"ResourceType=instance,Tags=[{{Key=Name,Value={name}}},{{Key=CNCCluster,Value={CLUSTER}}}]"
    arguments = ["ec2", "run-instances", "--image-id", ami,
                 "--instance-type", TYPE if controller_profile else WORKER_TYPE,
                 "--count", "1", "--key-name", key_name, "--subnet-id", plan["subnet_id"],
                 "--security-group-ids", group_id, "--associate-public-ip-address",
                 "--block-device-mappings", json.dumps(mappings), "--metadata-options",
                 "HttpEndpoint=enabled,HttpTokens=required", "--tag-specifications", tag,
                 "--client-token", token]
    arguments.extend(["--user-data", "#!/bin/bash\nset -eu\n" + key_only_setup('ubuntu')])
    if controller_profile:
        arguments.extend(["--iam-instance-profile", "Name=" + controller_profile])
    response = aws(*arguments)
    return response["Instances"][0]["InstanceId"]


def write_ssh(state):
    nodes = state["nodes"]
    key = deployment_key(ROOT, STATE_DIR)
    lines = []
    for name, node in nodes.items():
        address = node["public_ip"] if name.endswith("-server") else node["private_ip"]
        jump = " ProxyJump cnc-aws-server\n" if "-worker-" in name else ""
        lines.append(f"Host {name}\n User ubuntu\n HostName {address}\n{jump}" + identity_lines(key, STATE_DIR / 'id_ed25519') + f" IdentitiesOnly yes\n BatchMode yes\n ConnectTimeout 10\n StrictHostKeyChecking accept-new\n UserKnownHostsFile {STATE_DIR / 'known_hosts'}\n")
    path = STATE_DIR / "ssh_config"
    path.write_text("\n".join(lines))
    path.chmod(0o600)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--workers", type=int,
                        help="Workers to launch now (default: as many as current quota allows, up to seven)")
    parser.add_argument("--accept-compute-hourly-usd", type=float,
                        help="Required for --apply; must be at least the current eight-node compute rate")
    parser.add_argument("--ssh-cidr", default="0.0.0.0/0", help="Optional IPv4 restriction; default accepts changing client IPs with Mac-key-only SSH")
    args = parser.parse_args()
    plan = inventory()
    print(json.dumps(plan, indent=2), flush=True)
    if not args.apply:
        return
    workers_now = plan["workers_launchable_now"] if args.workers is None else args.workers
    if workers_now < 1 or workers_now > WORKERS:
        raise RuntimeError("Worker count must be from one through seven")
    required_vcpus = len(names(workers_now)) * plan["vcpus_per_instance"]
    if required_vcpus > plan["vcpus_quota"]:
        raise RuntimeError(f"Selected nodes require {required_vcpus} vCPUs; current quota is {plan['vcpus_quota']}")
    selected_hourly = plan["controller_hourly_usd"] + workers_now * plan["worker_hourly_usd"]
    if args.accept_compute_hourly_usd is None or args.accept_compute_hourly_usd < selected_hourly - 0.000001:
        raise RuntimeError("--accept-compute-hourly-usd must cover the displayed compute rate; storage and IPs cost extra")
    cidr = args.ssh_cidr
    import ipaddress
    network = ipaddress.ip_network(cidr, strict=True)
    if network.version != 4:
        raise RuntimeError("--ssh-cidr must be an IPv4 network")
    state = state_load()
    if state.get("account", plan["account"]) != plan["account"] or state.get("region", REGION) != REGION:
        raise RuntimeError("Existing local state belongs to another AWS project or Region")
    state.update(account=plan["account"], region=REGION, subnet_id=plan["subnet_id"],
                 vpc_id=plan["vpc_id"], instance_type=TYPE)
    state_save(state)
    key_name, _ = ensure_key(state)
    controller_group = ensure_group(state, "cnc-aws-controller", "CNC controller private services", plan["vpc_id"])
    worker_group = ensure_group(state, "cnc-aws-workers", "CNC worker SSH from controller", plan["vpc_id"])
    allow_ingress(controller_group, "tcp", 22, cidr)
    allow_ingress(controller_group, "tcp", 8080, worker_group)
    allow_ingress(controller_group, "tcp", 9090, worker_group)
    allow_ingress(worker_group, "tcp", 22, controller_group)
    ami, root_device = image()
    state.update(ami=ami, root_device=root_device, worker_security_group=worker_group)
    state_save(state)
    controller_profile = ensure_controller_role(state, plan["account"], plan["subnet_id"],
                                                worker_group, key_name, ami)
    state.setdefault("tokens", {})
    for name in names(workers_now):
        existing = active_instances().get(name)
        if existing:
            expected_type = TYPE if name.endswith("-server") else WORKER_TYPE
            if existing["InstanceType"] != expected_type or existing["ImageId"] != ami:
                raise RuntimeError(f"Existing {name} does not match the requested size or AMI")
            instance_id = existing["InstanceId"]
        else:
            token = state["tokens"].setdefault(name, secrets.token_hex(16))
            state_save(state)
            instance_id = launch(name, controller_group if name.endswith("-server") else worker_group,
                                 plan, key_name, ami, root_device, token, 40,
                                 controller_profile if name.endswith("-server") else None)
        print(f"{name}: {instance_id}", flush=True)
        state.setdefault("nodes", {})[name] = {"id": instance_id}
        state_save(state)
    aws("ec2", "wait", "instance-running", "--instance-ids", *(state["nodes"][name]["id"] for name in names(workers_now)))
    current = active_instances()
    for name in names(workers_now):
        item = current[name]
        state["nodes"][name].update(public_ip=item.get("PublicIpAddress"), private_ip=item["PrivateIpAddress"])
        if not state["nodes"][name]["public_ip"]:
            raise RuntimeError(f"{name} has no public IP for bootstrap; inspect its subnet settings")
    state_save(state)
    write_ssh(state)
    print(json.dumps({"created_or_reused": len(names(workers_now)),
                      "workers_now": workers_now, "workers_target": WORKERS,
                      "ssh_config": str(STATE_DIR / "ssh_config")}), flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Provisioning stopped: {error}", file=sys.stderr)
        sys.exit(1)
