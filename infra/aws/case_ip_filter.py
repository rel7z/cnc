"""Per-job stdout filter; changes no tool or deployment defaults."""
import shlex

FILTER = '''import ipaddress,sys
for line in sys.stdin:
    try:
        ipaddress.ip_address(line.strip())
    except ValueError:
        sys.stdout.write(line)
        sys.stdout.flush()
'''

def filtered_command(arguments, output_file):
    pipeline = shlex.join(arguments) + ' | python3 -u -c ' + shlex.quote(FILTER) + ' > ' + shlex.quote(output_file)
    return 'bash -o pipefail -c ' + shlex.quote(pipeline)
