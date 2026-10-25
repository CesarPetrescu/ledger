"""Check the rendered deployment without printing environment secrets."""
import json
import subprocess

config = json.loads(subprocess.check_output([
    "docker", "compose", "-f", "docker-compose.yml", "-f", "compose.sparktunnel.yml",
    "config", "--format", "json",
]))
services = config["services"]
for name, service in services.items():
    assert not service.get("ports"), f"{name} publishes a host port"
    assert service.get("network_mode") != "host", f"{name} uses host networking"
    if name != "spark-tunnel":
        assert "SPARK_TUNNEL_TOKEN" not in service.get("environment", {}), f"{name} receives the connector token"
connector = services["spark-tunnel"]
address = connector["networks"]["ledger-internal"]["ipv4_address"]
assert services["nginx"]["environment"]["LEDGER_TRUSTED_PROXY_CIDR"] == address + "/32"
assert connector["environment"]["SPARK_TUNNEL_TOKEN"]
assert connector["restart"] == "unless-stopped"
print("SparkTunnel: no published ports; token confined to connector; nginx trusts only connector.")
