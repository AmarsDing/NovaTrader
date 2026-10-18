"""Offline human-label evaluation; synthetic fixtures can never satisfy admission."""
import math
import random
import statistics
from .protocol import Invalid, finite, timestamp


def evaluate(job, data, output):
    if data.get("kind") != "jev_labels" or data.get("label_schema") != "public-news-labels-v1":
        raise Invalid("versioned Jev labels required")
    rows = data.get("rows", [])
    split = timestamp(data.get("test_start"))
    if not rows or len(rows) > 100000:
        raise Invalid("missing or excessive label set")
    ids = set(); groups = {}
    for row in rows:
        if not row.get("id") or row["id"] in ids:
            raise Invalid("duplicate label identity")
        ids.add(row["id"])
        if type(row.get("label")) is not bool or not row.get("stratum"):
            raise Invalid("binary label and stratum required")
        when = timestamp(row.get("published_at"))
        if when > timestamp(job["as_of"]):
            raise Invalid("future labeled item")
        expected = "test" if when >= split else "development"
        if row.get("split") != expected:
            raise Invalid("time split mismatch")
        group = row.get("cluster_id", row["id"])
        if group in groups and groups[group] != expected:
            raise Invalid("same event cluster leaks across development/test")
        groups[group] = expected
        if row.get("annotation") not in {"human", "synthetic"}:
            raise Invalid("annotation must explicitly distinguish human and synthetic")
        if row.get("annotation") == "human" and (not row.get("annotator") or not row.get("reviewed_by") or row["annotator"] == row["reviewed_by"]):
            raise Invalid("human labels require annotator and independent review")
    test = [r for r in rows if r["split"] == "test"]
    if not test:
        raise Invalid("sealed test partition is empty")
    lower = finite(data.get("abstain_lower", .35), "abstain_lower")
    upper = finite(data.get("abstain_upper", .65), "abstain_upper")
    if not 0 <= lower < upper <= 1:
        raise Invalid("invalid fixed abstention thresholds")
    methods = data.get("methods", ["jev", "rule", "local_model"])
    if not methods or len(set(methods)) != len(methods):
        raise Invalid("unique comparison methods required")

    def scores(items, method):
        observations = []; accepted = []; durations = []; usage = 0
        for row in items:
            pred = row.get("predictions", {}).get(method)
            if not pred or pred.get("abstain_reason"):
                continue
            p = finite(pred.get("probability"), "probability")
            if not 0 <= p <= 1:
                raise Invalid("probability outside [0,1]")
            if method == "jev" and (not pred.get("actual_model") or not pred.get("input_hash")):
                raise Invalid("Jev predictions require actual model and input hash")
            observations.append((p, int(row["label"])))
            if p <= lower or p >= upper:
                accepted.append((int(p >= upper), int(row["label"])))
            if pred.get("duration_ms") is not None:
                duration = finite(pred["duration_ms"], "duration_ms")
                if duration < 0: raise Invalid("negative duration")
                durations.append(duration)
            usage += max(0, int(pred.get("input_tokens", 0)))
        tp=sum(a==b==1 for a,b in accepted);fp=sum(a==1 and b==0 for a,b in accepted)
        fn=sum(a==0 and b==1 for a,b in accepted);tn=sum(a==b==0 for a,b in accepted)
        def ratio(a,b):return a/b if b else None
        precision=ratio(tp,tp+fp);recall=ratio(tp,tp+fn)
        f1p=ratio(2*tp,2*tp+fp+fn);f1n=ratio(2*tn,2*tn+fp+fn)
        brier=statistics.mean((p-y)**2 for p,y in observations) if observations else None
        ece=0.0
        for bucket in range(10):
            selected=[(p,y) for p,y in observations if min(int(p*10),9)==bucket]
            if selected:ece+=len(selected)/len(observations)*abs(statistics.mean(p for p,y in selected)-statistics.mean(y for p,y in selected))
        ordered=sorted(durations)
        def percentile(q):return ordered[min(len(ordered)-1,math.ceil(q*len(ordered))-1)] if ordered else None
        return {"sample_count":len(items),"answered":len(observations),"accepted":len(accepted),"coverage":len(accepted)/len(items),"precision":precision,"recall":recall,"macro_f1":(f1p+f1n)/2 if f1p is not None and f1n is not None else None,"brier":brier,"ece_10_bins":ece if observations else None,"confusion_accepted":{"tp":tp,"fp":fp,"fn":fn,"tn":tn},"latency_ms":{"p50":percentile(.5),"p95":percentile(.95),"p99":percentile(.99)},"input_tokens":usage}
    results={method:scores(test,method) for method in methods}
    # Bootstrap the sealed test outcomes without altering thresholds.
    seed=int(job.get("seed",0)); rng=random.Random(seed)
    for method in methods:
        values=[]
        for _ in range(200):
            value=scores([rng.choice(test) for _ in test],method)["brier"]
            if value is not None:values.append(value)
        values.sort();results[method]["brier_bootstrap_95"]=[values[int((len(values)-1)*.025)],values[int((len(values)-1)*.975)]] if values else None
    human=sum(row.get("annotation")=="human" for row in rows)
    strata={stratum:{method:scores([r for r in test if r["stratum"]==stratum],method) for method in methods} for stratum in sorted({r["stratum"] for r in test})}
    return {"methods":results,"strata":strata,"human_reviewed_count":human,"synthetic_count":sum(r.get("annotation")=="synthetic" for r in rows),"minimum_human_labels_met":human>=500,"thresholds_frozen":{"lower":lower,"upper":upper},"test_start":data["test_start"],"promotion":"shadow_only; no automatic trading or production scoring approval","warnings":["500 reviewed real Chinese labels required"] if human<500 else []}
