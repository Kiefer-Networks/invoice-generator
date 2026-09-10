#!/usr/bin/env python3
import gzip
import hashlib
import importlib.util
import io
import json
import pathlib
import tarfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("check-runtime-fresh.py")
SPEC = importlib.util.spec_from_file_location("runtime_fresh", SCRIPT)
runtime_fresh = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runtime_fresh)


class RuntimeFreshnessTests(unittest.TestCase):
    def test_parses_exact_runtime_and_direct_package_pins(self):
        pin = runtime_fresh.parse_dockerfile(
            b"""FROM alpine:3.24.1@sha256:""" + b"a" * 64 + b""" AS runtime
RUN apk add --no-cache ca-certificates=1-r0 chromium=2-r1 && echo done
FROM runtime AS production
"""
        )
        self.assertEqual(pin.tag, "3.24.1")
        self.assertEqual(pin.branch, "v3.24")
        self.assertEqual(pin.packages, {"ca-certificates": "1-r0", "chromium": "2-r1"})

    def test_rejects_mutable_base_or_unpinned_package(self):
        for dockerfile in (
            b"FROM alpine:3.24.1 AS runtime\nRUN apk add curl=1-r0\n",
            b"FROM alpine:3.24.1@sha256:" + b"a" * 64 + b" AS runtime\nRUN apk add curl\n",
        ):
            with self.subTest(dockerfile=dockerfile):
                with self.assertRaises(runtime_fresh.FreshnessError):
                    runtime_fresh.parse_dockerfile(dockerfile)

    def test_parses_both_lock_files_referenced_by_runtime(self):
        dockerfile = b"""FROM alpine:3.24.1@sha256:""" + b"a" * 64 + b""" AS runtime
COPY docker/apk-lock.amd64 docker/apk-lock.arm64 /usr/local/share/
RUN /usr/local/bin/install-locked-apks \"$TARGETARCH\"
FROM runtime AS visual
RUN apk add --no-cache poppler-utils=9-r0
"""
        locks = {
            "docker/apk-lock.amd64": b"ca-certificates=1-r0\ncurl=2-r0\n",
            "docker/apk-lock.arm64": b"ca-certificates=1-r0\ncurl=2-r0\n",
        }
        pin = runtime_fresh.parse_dockerfile(dockerfile, locks.__getitem__)
        self.assertEqual(pin.packages_by_arch["x86_64"]["curl"], "2-r0")
        self.assertEqual(pin.packages_by_arch["aarch64"]["curl"], "2-r0")

    def test_manifest_digest_and_required_platforms_fail_closed(self):
        manifests = [
            {"platform": {"os": "linux", "architecture": "amd64"}},
            {"platform": {"os": "linux", "architecture": "arm64", "variant": "v8"}},
        ]
        body = json.dumps({"schemaVersion": 2, "manifests": manifests}).encode()
        digest = "sha256:" + hashlib.sha256(body).hexdigest()
        runtime_fresh.verify_manifest(body, {"Docker-Content-Digest": digest}, digest)
        missing_platform = json.dumps({"schemaVersion": 2, "manifests": manifests[:1]}).encode()
        missing_digest = "sha256:" + hashlib.sha256(missing_platform).hexdigest()
        for bad_body, bad_headers, bad_pin in (
            (body, {}, digest),
            (body, {"Docker-Content-Digest": digest}, "sha256:" + "0" * 64),
            (missing_platform, {"Docker-Content-Digest": missing_digest}, missing_digest),
        ):
            with self.subTest():
                with self.assertRaises(runtime_fresh.FreshnessError):
                    runtime_fresh.verify_manifest(bad_body, bad_headers, bad_pin)

    def test_verifies_embedded_apkindex_signature_and_key_pin(self):
        # Deterministic 512-bit RSA fixture. Production verifies Alpine's pinned
        # 4096-bit repository keys; this only exercises PKCS#1 v1.5 SHA-1 parsing.
        n = int("8180efddefc0af669810e5552fe5a826ffc0231635653f4a8e0aa214de002871f0964004feceb68df6ddd04374c69dd9e997ba5e5b52ccae3481bce6e371d8f5", 16)
        e = 65537
        d = int("7858c11027289217ae432d4b9fea34fca0f905e23296b75d6a68993cf91d7e772970ab88ddfbb8c8806bdd7895fe347f4a9e4861c1405488a18aa907f210ef41", 16)
        payload = make_tar({"DESCRIPTION": b"test\n", "APKINDEX": b"P:curl\nV:8.22.0-r0\nt:1\n\n"})
        signed_member = gzip.compress(payload, mtime=0)
        digest_info = bytes.fromhex("3021300906052b0e03021a05000414") + hashlib.sha1(signed_member).digest()
        encoded = b"\x00\x01" + b"\xff" * (64 - len(digest_info) - 3) + b"\x00" + digest_info
        signature = pow(int.from_bytes(encoded), d, n).to_bytes(64, "big")
        key = der_public_key(n, e)
        key_name = "fixture.rsa.pub"
        signature_member = gzip.compress(make_tar({".SIGN.RSA." + key_name: signature}), mtime=0)
        archive = signature_member + signed_member
        key_hashes = {key_name: hashlib.sha256(key).hexdigest()}

        packages = runtime_fresh.verify_apkindex(archive, lambda _: key, key_hashes)
        self.assertEqual(packages["curl"], "8.22.0-r0")
        with self.assertRaises(runtime_fresh.FreshnessError):
            runtime_fresh.verify_apkindex(archive, lambda _: key, {key_name: "0" * 64})
        with self.assertRaises(runtime_fresh.FreshnessError):
            runtime_fresh.verify_apkindex(archive[:-1] + bytes([archive[-1] ^ 1]), lambda _: key, key_hashes)

    def test_requires_each_pin_to_be_current_on_both_architectures(self):
        pins = {"curl": "8.22.0-r0", "chromium": "152-r0"}
        runtime_fresh.check_package_pins(pins, {
            "x86_64": dict(pins),
            "aarch64": dict(pins),
        })
        with self.assertRaises(runtime_fresh.FreshnessError):
            runtime_fresh.check_package_pins(pins, {
                "x86_64": dict(pins),
                "aarch64": {"curl": "8.22.1-r0", "chromium": "152-r0"},
            })

    def test_selects_all_declared_direct_packages_from_each_lock(self):
        locks = {
            arch: {name: "1-r0" for name in runtime_fresh.DIRECT_PACKAGES}
            for arch in runtime_fresh.ARCHITECTURES
        }
        selected = runtime_fresh.select_direct_pins(locks)
        self.assertEqual(set(selected["x86_64"]), set(runtime_fresh.DIRECT_PACKAGES))
        del locks["aarch64"][runtime_fresh.DIRECT_PACKAGES[0]]
        with self.assertRaises(runtime_fresh.FreshnessError):
            runtime_fresh.select_direct_pins(locks)

    def test_apkindex_chooses_newest_build_and_rejects_ambiguous_ties(self):
        index = b"P:zfs-virt\nV:1-r0\nt:100\n\nP:zfs-virt\nV:2-r0\nt:200\n"
        self.assertEqual(runtime_fresh.parse_apkindex(index)["zfs-virt"], "2-r0")
        with self.assertRaises(runtime_fresh.FreshnessError):
            runtime_fresh.parse_apkindex(b"P:curl\nV:1-r0\nt:100\n\nP:curl\nV:2-r0\nt:100\n")


def make_tar(files):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w") as archive:
        for name, contents in files.items():
            info = tarfile.TarInfo(name)
            info.size = len(contents)
            info.mode = 0o644
            info.mtime = 0
            archive.addfile(info, io.BytesIO(contents))
    return out.getvalue()


def der_length(length):
    if length < 128:
        return bytes([length])
    raw = length.to_bytes((length.bit_length() + 7) // 8, "big")
    return bytes([0x80 | len(raw)]) + raw


def der(tag, contents):
    return bytes([tag]) + der_length(len(contents)) + contents


def der_integer(value):
    raw = value.to_bytes((value.bit_length() + 7) // 8, "big")
    if raw[0] & 0x80:
        raw = b"\x00" + raw
    return der(0x02, raw)


def der_public_key(n, e):
    rsa = der(0x30, der_integer(n) + der_integer(e))
    algorithm = bytes.fromhex("300d06092a864886f70d0101010500")
    spki = der(0x30, algorithm + der(0x03, b"\x00" + rsa))
    import base64
    encoded = base64.b64encode(spki)
    return b"-----BEGIN PUBLIC KEY-----\n" + encoded + b"\n-----END PUBLIC KEY-----\n"


if __name__ == "__main__":
    unittest.main()
