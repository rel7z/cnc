#!/usr/bin/env python3
"""Install the private cross-cloud endpoint; no jobs or private tool access."""
import argparse,json,os,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
STATE=ROOT/'.aws-cnc'
AWS=str(Path.home()/'.local/bin/aws')
def aws(*args):
 r=subprocess.run([AWS,*args,'--profile','cnc','--region','us-east-2','--output','json'],capture_output=True,text=True)
 if r.returncode:raise RuntimeError('AWS operation failed: '+r.stderr[:400])
 return json.loads(r.stdout) if r.stdout.strip() else {}
def main():
 p=argparse.ArgumentParser();p.add_argument('--apply',action='store_true');a=p.parse_args()
 state=json.loads((STATE/'state.json').read_text());instance=state['nodes']['cnc-aws-server']['id']
 node=aws('ec2','describe-instances','--instance-ids',instance)['Reservations'][0]['Instances'][0]
 if not a.apply:print(json.dumps({'controller':instance,'endpoint':node['PublicIpAddress']+':51820','ingress':'individual Linode worker IPv4 /32 only','listeners':'existing private addresses','role_permission':'authorize ingress only on dedicated WireGuard security group'}));return
 groups=aws('ec2','describe-security-groups','--filters','Name=group-name,Values=cnc-linode-wireguard',f"Name=vpc-id,Values={node['VpcId']}")['SecurityGroups']
 if len(groups)>1:raise RuntimeError('Ambiguous WireGuard security group')
 if groups:group=groups[0]['GroupId']
 else:
  group=aws('ec2','create-security-group','--group-name','cnc-linode-wireguard','--description','CNC encrypted Linode workers, exact source IP rules','--vpc-id',node['VpcId'],'--tag-specifications','ResourceType=security-group,Tags=[{Key=CNCCluster,Value=cnc-aws},{Key=Purpose,Value=linode-wireguard}]')['GroupId']
 current=[g['GroupId'] for g in node['SecurityGroups']]
 if group not in current:aws('ec2','modify-instance-attribute','--instance-id',instance,'--groups',*current,group)
 account=aws('sts','get-caller-identity')['Account']
 policy={'Version':'2012-10-17','Statement':[{'Effect':'Allow','Action':['ec2:AuthorizeSecurityGroupIngress'],'Resource':f'arn:aws:ec2:us-east-2:{account}:security-group/{group}'},{'Effect':'Allow','Action':['ec2:DescribeSecurityGroups'],'Resource':'*'}]}
 aws('iam','put-role-policy','--role-name',state['controller_role'],'--policy-name','cnc-linode-wireguard-ingress','--policy-document',json.dumps(policy))
 record={'controller':instance,'endpoint':node['PublicIpAddress']+':51820','security_group':group,'previous_security_groups':current,'role':state['controller_role']}
 out=STATE/'linode-network.json';out.write_text(json.dumps(record,indent=2));out.chmod(0o600)
 script=ROOT/'infra/aws/setup-linode-network-remote.py'
 content=script.read_text().replace('NETWORK_PAYLOAD',repr(json.dumps(record)))
 command=aws('ssm','send-command','--instance-ids',instance,'--document-name','AWS-RunShellScript','--comment','CNC authorized encrypted Linode network','--parameters',json.dumps({'commands':["python3 - <<'NETWORK'\n"+content+"\nNETWORK\n"],'executionTimeout':['600']}))['Command']['CommandId']
 record['setup_command_id']=command;out.write_text(json.dumps(record,indent=2));print(json.dumps({'security_group':group,'setup_command_id':command,'public_raw_services_opened':False}))
if __name__=='__main__':main()
