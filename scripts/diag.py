import sys, platform
print("python:", sys.version.split()[0], platform.machine())
for pkg in ("camoufox", "browserforge", "apify_fingerprint_datapoints", "curl_cffi"):
    try:
        import importlib.metadata as m
        print(f"{pkg}:", m.version(pkg))
    except Exception as e:
        print(f"{pkg}: MISSING ({e})")

print("--- browserforge header gen ---")
try:
    from browserforge.headers import HeaderGenerator
    h = HeaderGenerator().generate(browser="firefox")
    print("firefox headers OK:", "user-agent" in {k.lower() for k in h})
except Exception as e:
    print("HEADER GEN FAILED:", repr(e))

print("--- browserforge fingerprint gen ---")
try:
    from browserforge.fingerprints import FingerprintGenerator
    fp = FingerprintGenerator().generate()
    print("fingerprint OK")
except Exception as e:
    print("FINGERPRINT FAILED:", repr(e))
