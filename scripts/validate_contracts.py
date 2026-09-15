"""Static artifact checks only; this is not application crypto or an API test suite."""
from __future__ import annotations

import base64
import json
import re
from pathlib import Path
from xml.etree import ElementTree

import yaml
from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

ROOT = Path(__file__).resolve().parents[1]
SOURCE_DIRS = [ROOT / name for name in ("docs", "contracts", "server", "clients", "deploy", "scripts")]


def resolve_ref(document: dict, reference: str):
    assert reference.startswith("#/"), f"Non-local reference: {reference}"
    value = document
    for part in reference[2:].split("/"):
        value = value[part.replace("~1", "/").replace("~0", "~")]
    return value


def walk_refs(node, document):
    if isinstance(node, dict):
        if "$ref" in node:
            resolve_ref(document, node["$ref"])
        for child in node.values():
            walk_refs(child, document)
    elif isinstance(node, list):
        for child in node:
            walk_refs(child, document)


def decode(value: str) -> bytes:
    result = base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))
    assert base64.urlsafe_b64encode(result).decode("ascii").rstrip("=") == value
    return result


def derive(ikm: bytes, salt: bytes, info: bytes) -> bytes:
    return HKDF(algorithm=hashes.SHA256(), length=32, salt=salt, info=info).derive(ikm)


def validate_vectors():
    fixture = json.loads((ROOT / "contracts/crypto-v1-vectors.json").read_text(encoding="utf-8"))
    cmk = bytes.fromhex(fixture["cmk_hex"])
    rk = bytes.fromhex(fixture["recovery_key_hex"])
    vault = fixture["vault_envelope"]
    wrap_aad = f"oc-v1|vault|{fixture['user_id']}|{vault['vault_id']}|1".encode()
    kek = derive(rk, decode(vault["wrap_salt"]), b"onlineclipboard/v1/wrap")
    assert kek.hex() == fixture["wrap_key_hex"]
    assert AESGCM(kek).encrypt(decode(vault["wrap_nonce"]), cmk, wrap_aad) == decode(vault["wrapped_key"])
    assert AESGCM(kek).decrypt(decode(vault["wrap_nonce"]), decode(vault["wrapped_key"]), wrap_aad) == cmk
    for case in fixture["items"]:
        envelope = case["envelope"]
        info = f"onlineclipboard/v1/item|{envelope['id']}|1".encode()
        key = derive(cmk, vault["vault_id"].encode(), info)
        aad = (f"oc-v1|clip|{fixture['user_id']}|{vault['vault_id']}|"
               f"{envelope['id']}|{envelope['source_device_id']}|1|text/plain").encode()
        plaintext = case["plaintext"].encode("utf-8")
        nonce = decode(envelope["nonce"])
        ciphertext = decode(envelope["ciphertext"])
        assert plaintext.hex() == case["plaintext_utf8_hex"]
        assert key.hex() == case["item_key_hex"]
        assert aad.decode() == case["aad_utf8"]
        assert AESGCM(key).encrypt(nonce, plaintext, aad) == ciphertext
        assert AESGCM(key).decrypt(nonce, ciphertext, aad) == plaintext
        corrupt = ciphertext[:-1] + bytes([ciphertext[-1] ^ 1])
        for bad_nonce, bad_ciphertext, bad_aad in (
            (nonce, corrupt, aad), (nonce, ciphertext, aad + b"x"),
            (bytes([nonce[0] ^ 1]) + nonce[1:], ciphertext, aad),
        ):
            try:
                AESGCM(key).decrypt(bad_nonce, bad_ciphertext, bad_aad)
            except InvalidTag:
                continue
            raise AssertionError("AEAD accepted a modified vector")
    return len(fixture["items"])


def main():
    source_files = [ROOT / "README.md"]
    for directory in SOURCE_DIRS:
        source_files.extend(p for p in directory.rglob("*") if p.is_file()
                            and not any(part in {"bin", "obj", "build", ".gradle", "__pycache__"} for part in p.parts))
    for path in source_files:
        if path.suffix not in {".md", ".yaml", ".json", ".xml", ".xaml", ".csproj", ".go", ".sql", ".kt", ".kts", ".py"}:
            continue
        content = path.read_text(encoding="utf-8")
        assert "\ufffd" not in content, f"Invalid replacement character: {path}"
        if path.suffix in {".xml", ".xaml", ".csproj"}:
            ElementTree.fromstring(content)
        elif path.suffix == ".yaml":
            yaml.safe_load(content)
        elif path.suffix == ".json":
            json.loads(content)
        elif path.suffix == ".md":
            for link in re.findall(r"\[[^\]]*\]\(([^)]+)\)", content):
                if re.match(r"[a-zA-Z]+://", link) or link.startswith("#"):
                    continue
                target = link.split("#", 1)[0]
                assert (path.parent / target).exists(), f"Broken link: {path}: {link}"

    api = yaml.safe_load((ROOT / "contracts/openapi.yaml").read_text(encoding="utf-8"))
    assert api["openapi"] == "3.0.3"
    walk_refs(api, api)
    operations = set()
    for path, methods in api["paths"].items():
        for method, operation in methods.items():
            assert method in {"get", "post", "put", "patch", "delete", "head", "options"}
            opid = operation["operationId"]
            assert opid not in operations, f"Duplicate operation: {opid}"
            operations.add(opid)
            assert operation["responses"]
            parameters = [resolve_ref(api, p["$ref"]) if "$ref" in p else p for p in operation.get("parameters", [])]
            for name in re.findall(r"\{([^}]+)\}", path):
                assert any(p["name"] == name and p["in"] == "path" and p.get("required") for p in parameters)
    vectors = validate_vectors()
    print(f"PASS: UTF-8, local documentation links, YAML/JSON/XML, {len(operations)} OpenAPI operations, {vectors} crypto vectors and tamper rejection.")
    print("Scope: static references/shape checks, not full OpenAPI conformance, SQL execution or client integration.")


if __name__ == "__main__":
    main()
