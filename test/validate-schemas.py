#!/usr/bin/env python3
"""Validates the shipped YAML files against schema/*.json (needs: pip install jsonschema pyyaml)."""
import glob, json, os, sys
import jsonschema, yaml
root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
jobs = [("omloader", f) for f in ["configs/defaults.yaml", *sorted(glob.glob("examples/*.yaml"))]]
jobs += [("sources", "configs/sources.yaml")] + [("image", f) for f in sorted(glob.glob("examples/*/image.yaml"))]
bad = 0
for sch, f in jobs:
    schema = json.load(open(os.path.join(root, "schema", sch + ".schema.json")))
    errs = list(jsonschema.Draft7Validator(schema).iter_errors(yaml.safe_load(open(os.path.join(root, f)))))
    print(("FAIL " if errs else "ok   ") + f + " (" + sch + ")")
    for e in errs[:5]:
        print("   ", "/".join(map(str, e.absolute_path)), e.message[:160])
    bad += bool(errs)
sys.exit(1 if bad else 0)
