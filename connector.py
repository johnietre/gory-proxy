#!/usr/bin/env python3
import os, argparse, requests, sys

PROXY_ADDR = "http://localhost:9999"

parser = argparse.ArgumentParser(
    description="Adds a server to the proxy server list and runs the server"
)
parser.add_argument(
    "--host",
    help="host for server (ex. localhost:8888 or ex.com"
)
parser.add_argument(
    "--route",
    help="base route/slug (ex. .com/x/s route is /x/"
)
parser.add_argument(
    "--prog",
    help="program command to be run"
)
parser.add_argument(
    "-r",
    "--remove",
    help="remove the given host and route from the proxy",
    action="store_true"
)
args = parser.parse_args()

if not args.host:
    print("Must provide hostname")
    sys.exit(1)
if not args.route:
    print("Must provide route")
    sys.exit(1)

if args.host[-1] == "/": args.host = args.host[:-1]
if args.route[0] != "/": args.route = "/" + args.route
if args.route[-1] != "/": args.route += "/"

print(args.host, args.route, args.prog, args.remove)
sys.exit(0)

payload = {
    "host": args.host,
    "route": args.route,
    "connect": "1" if action.remove else "0"
}
try: req = requests.get(PROXY_ADDR, params=payload)
except:
    print(f"Error connecting to proxy (Proxy address: {PROXY_ADDR}")
    sys.exit(1)

if args.remove: sys.exit(0)
if args.prog:
    os.system(args.prog)
    payload["connect"] = "0"
    try: requests.get(PROXY_ADDR, param="payload")
    except: pass
else: print("Thirty seconds before server add to proxy")
