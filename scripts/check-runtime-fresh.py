#!/usr/bin/env python3
"""Fail closed when the pinned Alpine runtime is no longer current."""

import base64
import dataclasses
import gzip
import hashlib
import io
import json
import pathlib
import re
import sys
import tarfile
import urllib.parse
import urllib.request
import zlib


REGISTRY = "https://registry-1.docker.io"
TOKEN_SERVICE = "https://auth.docker.io/token"
ALPINE_MIRROR = "https://dl-cdn.alpinelinux.org/alpine"
ALPINE_KEYS = "https://alpinelinux.org/keys"
ARCHITECTURES = ("x86_64", "aarch64")
REPOSITORIES = ("main", "community")
DIRECT_PACKAGES = (
    "ca-certificates",
    "chromium",
    "openjdk21-jdk",
    "font-liberation",
    "curl",
    "libcrypto3",
    "libssl3",
)
KEY_SHA256 = {
    "alpine-devel@lists.alpinelinux.org-6165ee59.rsa.pub": "207e4696d3c05f7cb05966aee557307151f1f00217af4143c1bcaf33b8df733f",
    "alpine-devel@lists.alpinelinux.org-616ae350.rsa.pub": "d11f6b21c61b4274e182eb888883a8ba8acdbf820dcc7a6d82a7d9fc2fd2836d",
}
MAX_MANIFEST = 2 << 20
MAX_INDEX = 16 << 20
MAX_KEY = 16 << 10
USER_AGENT = "invoice-generator-runtime-freshness/1"


class FreshnessError(RuntimeError):
    pass


@dataclasses.dataclass(frozen=True)
class RuntimePin:
    tag: str
    branch: str
    digest: str
    packages_by_arch: dict[str, dict[str, str]]

    @property
    def packages(self) -> dict[str, str]:
        """Compatibility view for Dockerfiles with one shared direct pin set."""
        values = list(self.packages_by_arch.values())
        return values[0] if values and all(item == values[0] for item in values) else {}


def _parse_lock(data: bytes, name: str) -> dict[str, str]:
    try:
        lines = data.decode("utf-8").splitlines()
    except UnicodeDecodeError as exc:
        raise FreshnessError(f"{name} is not UTF-8") from exc
    if not lines or any(not line for line in lines):
        raise FreshnessError(f"{name} must be a nonempty lock file")
    packages = {}
    for item in lines:
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9+_.-]*=[A-Za-z0-9][A-Za-z0-9+_.~-]*", item):
            raise FreshnessError(f"invalid exact apk lock entry in {name}: {item}")
        package, version = item.split("=", 1)
        if package in packages:
            raise FreshnessError(f"duplicate apk lock entry in {name}: {package}")
        packages[package] = version
    return packages


def parse_dockerfile(data: bytes, lock_loader=None) -> RuntimePin:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise FreshnessError("Dockerfile is not UTF-8") from exc
    match = re.search(
        r"(?m)^FROM alpine:(\d+\.\d+\.\d+)@(sha256:[0-9a-f]{64}) AS runtime\s*$",
        text,
    )
    if not match:
        raise FreshnessError("runtime must use an exact Alpine patch tag and SHA-256 digest")
    tag, digest = match.groups()
    branch = "v" + ".".join(tag.split(".")[:2])
    next_stage = re.search(r"(?m)^FROM ", text[match.end():])
    runtime_end = match.end() + next_stage.start() if next_stage else len(text)
    runtime_text = text[match.end():runtime_end]
    logical = re.sub(r"\\\r?\n", " ", runtime_text)
    apk = re.search(r"(?m)^RUN apk add --no-cache\s+(.+?)(?:\s+&&|\s*$)", logical)
    if apk:
        packages = _parse_lock(("\n".join(apk.group(1).split()) + "\n").encode(), "Dockerfile")
        return RuntimePin(tag, branch, digest, {arch: dict(packages) for arch in ARCHITECTURES})
    required_copy = "COPY docker/apk-lock.amd64 docker/apk-lock.arm64 /usr/local/share/"
    required_run = 'RUN /usr/local/bin/install-locked-apks "$TARGETARCH"'
    if required_copy not in runtime_text or required_run not in logical or lock_loader is None:
        raise FreshnessError("runtime must install exact pins from both reviewed architecture locks")
    try:
        by_arch = {
            "x86_64": _parse_lock(lock_loader("docker/apk-lock.amd64"), "docker/apk-lock.amd64"),
            "aarch64": _parse_lock(lock_loader("docker/apk-lock.arm64"), "docker/apk-lock.arm64"),
        }
    except (KeyError, OSError) as exc:
        raise FreshnessError("runtime apk lock file is missing") from exc
    return RuntimePin(tag, branch, digest, by_arch)


def _header(headers, name: str) -> str:
    if hasattr(headers, "get"):
        value = headers.get(name)
        if value is None:
            value = headers.get(name.lower())
        return value or ""
    return ""


def verify_manifest(body: bytes, headers, expected_digest: str) -> None:
    actual = "sha256:" + hashlib.sha256(body).hexdigest()
    declared = _header(headers, "Docker-Content-Digest")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", declared):
        raise FreshnessError("registry omitted a valid immutable manifest digest")
    if actual != declared or declared != expected_digest:
        raise FreshnessError(
            f"Alpine tag digest changed: Dockerfile {expected_digest}, registry {declared}, content {actual}"
        )
    try:
        document = json.loads(body)
        manifests = document["manifests"]
    except (UnicodeDecodeError, json.JSONDecodeError, KeyError, TypeError) as exc:
        raise FreshnessError("registry returned an invalid image index") from exc
    if document.get("schemaVersion") != 2 or not isinstance(manifests, list):
        raise FreshnessError("registry response is not a schema-2 image index")
    platforms = {
        (item.get("platform", {}).get("os"), item.get("platform", {}).get("architecture"), item.get("platform", {}).get("variant", ""))
        for item in manifests
        if isinstance(item, dict) and isinstance(item.get("platform"), dict)
    }
    if ("linux", "amd64", "") not in platforms or not any(
        os_name == "linux" and arch == "arm64" and variant in ("", "v8")
        for os_name, arch, variant in platforms
    ):
        raise FreshnessError("Alpine image index lacks linux/amd64 or linux/arm64/v8")


def _read_url(url: str, *, headers=None, limit: int) -> tuple[bytes, object]:
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT, **(headers or {})})
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            if response.status != 200 or response.geturl() != url:
                raise FreshnessError(f"unexpected response for {url}")
            data = response.read(limit + 1)
            if len(data) > limit:
                raise FreshnessError(f"response exceeds size limit: {url}")
            return data, response.headers
    except FreshnessError:
        raise
    except Exception as exc:
        raise FreshnessError(f"request failed: {url}: {exc}") from exc


def fetch_manifest(tag: str, expected_digest: str) -> None:
    query = urllib.parse.urlencode({
        "service": "registry.docker.io",
        "scope": "repository:library/alpine:pull",
    })
    body, _ = _read_url(f"{TOKEN_SERVICE}?{query}", limit=64 << 10)
    try:
        token_document = json.loads(body)
        token = token_document["token"]
    except (UnicodeDecodeError, json.JSONDecodeError, KeyError, TypeError) as exc:
        raise FreshnessError("Docker Hub returned an invalid token response") from exc
    if not isinstance(token, str) or len(token) < 32:
        raise FreshnessError("Docker Hub returned an invalid token")
    manifest, headers = _read_url(
        f"{REGISTRY}/v2/library/alpine/manifests/{urllib.parse.quote(tag, safe='')}",
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json",
        },
        limit=MAX_MANIFEST,
    )
    verify_manifest(manifest, headers, expected_digest)


def _split_gzip(data: bytes) -> tuple[bytes, bytes]:
    try:
        stream = zlib.decompressobj(16 + zlib.MAX_WBITS)
        stream.decompress(data)
        stream.flush()
    except zlib.error as exc:
        raise FreshnessError("APKINDEX has invalid gzip framing") from exc
    if not stream.eof or not stream.unused_data:
        raise FreshnessError("APKINDEX must contain separate signature and data streams")
    split = len(data) - len(stream.unused_data)
    return data[:split], data[split:]


def _tar_files(compressed: bytes) -> dict[str, bytes]:
    try:
        with tarfile.open(fileobj=io.BytesIO(compressed), mode="r:gz") as archive:
            result = {}
            for member in archive.getmembers():
                if not member.isfile() or member.name.startswith("/") or ".." in pathlib.PurePosixPath(member.name).parts:
                    continue
                handle = archive.extractfile(member)
                if handle is None:
                    raise FreshnessError("APKINDEX contains an unreadable member")
                result[member.name] = handle.read(MAX_INDEX + 1)
            return result
    except (tarfile.TarError, OSError) as exc:
        raise FreshnessError("APKINDEX has invalid tar framing") from exc


def _der_item(data: bytes, offset: int) -> tuple[int, bytes, int]:
    if offset + 2 > len(data):
        raise FreshnessError("truncated Alpine public key")
    tag = data[offset]
    length = data[offset + 1]
    offset += 2
    if length & 0x80:
        count = length & 0x7F
        if count == 0 or count > 4 or offset + count > len(data):
            raise FreshnessError("invalid Alpine public-key length")
        length = int.from_bytes(data[offset:offset + count], "big")
        offset += count
    end = offset + length
    if end > len(data):
        raise FreshnessError("truncated Alpine public-key value")
    return tag, data[offset:end], end


def _rsa_public_numbers(pem: bytes) -> tuple[int, int]:
    match = re.fullmatch(
        rb"-----BEGIN PUBLIC KEY-----\s+([A-Za-z0-9+/=\r\n]+)-----END PUBLIC KEY-----\s*",
        pem,
    )
    if not match:
        raise FreshnessError("Alpine key is not a PEM SubjectPublicKeyInfo key")
    try:
        der = base64.b64decode(match.group(1), validate=False)
        tag, spki, end = _der_item(der, 0)
        if tag != 0x30 or end != len(der):
            raise FreshnessError("invalid Alpine SubjectPublicKeyInfo")
        tag, algorithm, pos = _der_item(spki, 0)
        if tag != 0x30 or bytes.fromhex("06092a864886f70d010101") not in algorithm:
            raise FreshnessError("Alpine key is not RSA")
        tag, bits, end = _der_item(spki, pos)
        if tag != 0x03 or end != len(spki) or not bits or bits[0] != 0:
            raise FreshnessError("invalid Alpine RSA bit string")
        tag, rsa, end = _der_item(bits, 1)
        if tag != 0x30 or end != len(bits):
            raise FreshnessError("invalid Alpine RSA key")
        tag, modulus, pos = _der_item(rsa, 0)
        tag_e, exponent, end = _der_item(rsa, pos)
        if tag != 0x02 or tag_e != 0x02 or end != len(rsa):
            raise FreshnessError("invalid Alpine RSA numbers")
        n, e = int.from_bytes(modulus, "big"), int.from_bytes(exponent, "big")
        if n.bit_length() < 512 or e < 3 or e % 2 == 0:
            raise FreshnessError("unsafe Alpine RSA key")
        return n, e
    except (ValueError, TypeError) as exc:
        raise FreshnessError("invalid Alpine public key") from exc


def _verify_pkcs1_sha1(message: bytes, signature: bytes, public_key: bytes) -> None:
    n, e = _rsa_public_numbers(public_key)
    width = (n.bit_length() + 7) // 8
    if len(signature) != width:
        raise FreshnessError("APKINDEX signature length does not match its key")
    decoded = pow(int.from_bytes(signature, "big"), e, n).to_bytes(width, "big")
    digest_info = bytes.fromhex("3021300906052b0e03021a05000414") + hashlib.sha1(message).digest()
    expected = b"\x00\x01" + b"\xff" * (width - len(digest_info) - 3) + b"\x00" + digest_info
    if decoded != expected:
        raise FreshnessError("APKINDEX RSA signature verification failed")


def verify_apkindex(data: bytes, key_fetcher, key_hashes=KEY_SHA256) -> dict[str, str]:
    signature_stream, index_stream = _split_gzip(data)
    signatures = _tar_files(signature_stream)
    names = [name for name in signatures if name.startswith(".SIGN.RSA.")]
    if len(names) != 1:
        raise FreshnessError("APKINDEX must contain exactly one RSA signature")
    key_name = names[0][len(".SIGN.RSA."):]
    expected_key_hash = key_hashes.get(key_name)
    if not expected_key_hash:
        raise FreshnessError(f"APKINDEX uses an unreviewed signing key: {key_name}")
    key = key_fetcher(key_name)
    if hashlib.sha256(key).hexdigest() != expected_key_hash:
        raise FreshnessError(f"Alpine signing key changed: {key_name}")
    _verify_pkcs1_sha1(index_stream, signatures[names[0]], key)
    files = _tar_files(index_stream)
    if set(files) != {"DESCRIPTION", "APKINDEX"} or len(files["APKINDEX"]) > MAX_INDEX:
        raise FreshnessError("signed APKINDEX payload has an unexpected schema")
    try:
        files["APKINDEX"].decode("utf-8")
    except UnicodeDecodeError as exc:
        raise FreshnessError("APKINDEX is not UTF-8") from exc
    return parse_apkindex(files["APKINDEX"])


def parse_apkindex(data: bytes) -> dict[str, str]:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise FreshnessError("APKINDEX is not UTF-8") from exc
    selected = {}
    for record in text.strip().split("\n\n"):
        fields = {}
        for line in record.splitlines():
            if len(line) >= 3 and line[1] == ":":
                fields[line[0]] = line[2:]
        name, version, built = fields.get("P"), fields.get("V"), fields.get("t")
        if not name or not version:
            continue
        if built is None or not built.isdigit():
            raise FreshnessError(f"package lacks a valid build timestamp: {name}")
        candidate = (int(built), version)
        previous = selected.get(name)
        if previous and previous[0] == candidate[0] and previous[1] != version:
            raise FreshnessError(f"ambiguous current package version: {name}")
        if previous is None or candidate[0] > previous[0]:
            selected[name] = candidate
    if not selected:
        raise FreshnessError("signed APKINDEX contains no packages")
    return {name: value[1] for name, value in selected.items()}


def _fetch_key(name: str) -> bytes:
    if not re.fullmatch(r"[A-Za-z0-9@._-]+\.rsa\.pub", name):
        raise FreshnessError("invalid Alpine signing-key name")
    return _read_url(f"{ALPINE_KEYS}/{name}", limit=MAX_KEY)[0]


def fetch_package_indexes(branch: str) -> dict[str, dict[str, str]]:
    result = {}
    for arch in ARCHITECTURES:
        merged = {}
        for repository in REPOSITORIES:
            url = f"{ALPINE_MIRROR}/{branch}/{repository}/{arch}/APKINDEX.tar.gz"
            data, _ = _read_url(url, limit=MAX_INDEX)
            packages = verify_apkindex(data, _fetch_key)
            overlap = merged.keys() & packages.keys()
            if overlap:
                raise FreshnessError(f"duplicate packages across Alpine repositories: {sorted(overlap)[0]}")
            merged.update(packages)
        result[arch] = merged
    return result


def check_package_pins(pins, indexes: dict[str, dict[str, str]]) -> None:
    if set(indexes) != set(ARCHITECTURES):
        raise FreshnessError("both Alpine architecture indexes are required")
    errors = []
    for arch in ARCHITECTURES:
        arch_pins = pins[arch] if set(pins) == set(ARCHITECTURES) else pins
        for name, pinned in arch_pins.items():
            current = indexes[arch].get(name)
            if current is None:
                errors.append(f"{arch}: {name} is absent")
            elif current != pinned:
                errors.append(f"{arch}: {name} {pinned} -> {current}")
    if errors:
        raise FreshnessError("runtime apk pins are not current:\n" + "\n".join(errors))


def select_direct_pins(locks: dict[str, dict[str, str]]) -> dict[str, dict[str, str]]:
    if set(locks) != set(ARCHITECTURES):
        raise FreshnessError("both Alpine architecture locks are required")
    selected = {}
    for arch in ARCHITECTURES:
        missing = [name for name in DIRECT_PACKAGES if name not in locks[arch]]
        if missing:
            raise FreshnessError(f"{arch} lock omits direct runtime package: {missing[0]}")
        selected[arch] = {name: locks[arch][name] for name in DIRECT_PACKAGES}
    return selected


def main() -> int:
    root = pathlib.Path(__file__).resolve().parent.parent
    try:
        pin = parse_dockerfile(
            (root / "Dockerfile").read_bytes(),
            lambda name: (root / name).read_bytes(),
        )
        fetch_manifest(pin.tag, pin.digest)
        direct_pins = select_direct_pins(pin.packages_by_arch)
        check_package_pins(direct_pins, fetch_package_indexes(pin.branch))
    except (FreshnessError, OSError) as exc:
        print(f"runtime freshness check failed: {exc}", file=sys.stderr)
        return 1
    print(f"Alpine {pin.tag} digest and all {len(DIRECT_PACKAGES)} direct apk pins are current for amd64 and arm64.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
