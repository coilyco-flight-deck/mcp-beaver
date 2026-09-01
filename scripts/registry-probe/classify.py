"""Re-classify a stored probe without re-probing.

Classify on the tool NAME's leading token only. Matching descriptions poisons
it: "the published docs" fires `publish`, "starting path" fires `start`, and
five plain reads on ac.tandem/docs-mcp came back mutating.
"""
import json, pathlib, re, sys

HERE = pathlib.Path(__file__).resolve().parent

READ = {"get","list","search","fetch","read","query","find","inspect","status",
        "describe","show","view","check","lookup","resolve","explain","answer",
        "recommend","suggest","compare","count","summarize","analyze","validate",
        "preview","browse","diff","calculate","estimate","score","detect","parse"}
WRITE = {"create","update","delete","write","refresh","invalidate","set","send",
         "post","execute","run","kill","inject","install","deploy","publish",
         "remove","add","clear","purge","warmup","reset","modify","edit","upload",
         "insert","drop","cancel","approve","submit","schedule","stop",
         "restart","move","rename","save","apply","trigger","sync","import",
         "register","revoke","grant","book","order","pay","buy","generate","make"}
# NB: "start" deliberately absent, see get_start_path.

def tokens(name):
    spaced = re.sub(r"([a-z0-9])([A-Z])", r"\1 \2", name.strip())
    return [t.lower() for t in re.split(r"[_.\-/ ]+", spaced) if t]

def head(name):
    t = tokens(name)
    return t[0] if t else ""

def klass(name):
    """Any write verb anywhere wins. A directory about blast radius should
    over-warn, and `compare_docs_index_refresh` does force a refresh."""
    t = set(tokens(name))
    if t & WRITE: return "mutates"
    if t & READ:  return "reads"
    return "unknown"

if __name__ == "__main__":
    path = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else HERE / "probe.json"
    d = json.load(path.open())
    for s in d:
        for t in (s.get("tools") or []):
            t["klass"] = klass(t["name"])
            t.pop("mutates", None)
    with path.open("w") as f:
        json.dump(d, f, indent=1)
        f.write("\n")
    tot = [t for s in d for t in (s.get("tools") or [])]
    from collections import Counter
    print("tools:", len(tot), dict(Counter(t["klass"] for t in tot)))
    print("unknown heads:", Counter(head(t["name"]) for t in tot if t["klass"]=="unknown").most_common(12))
