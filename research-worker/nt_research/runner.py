from __future__ import annotations
import json
import os
from pathlib import Path
import sys
import time
from .protocol import Invalid, Unavailable, canonical, check_job, digest, environment, sha
from .analytics import performance, portfolio, vector_screen
from .evaluate import evaluate
from .sandbox import rd_agent
from .prediction import archive, evaluate_predictions


def run(job_path:Path,output_root:Path):
    job_path=job_path.resolve();root=job_path.parent
    job=json.loads(job_path.read_text(encoding="utf-8"))
    manifest,data=check_job(job,root)
    output_root=output_root.resolve();output_root.mkdir(parents=True,exist_ok=True)
    output=output_root/job["run_id"]
    output.mkdir(exist_ok=False) # An existing experiment is immutable, including failures.
    result={"schema_version":1,"run_id":job["run_id"],"kind":job["kind"],"input_hash":digest(job),"manifest_id":manifest["manifest_id"],"manifest_hash":manifest["hash"],"manifest_file_sha256":job["manifest"]["sha256"],"environment":environment(),"seed":job.get("seed",0),"status":"running","result":None,"artifacts":[]}
    (output/"request.json").write_bytes(canonical(job))
    started=time.monotonic()
    try:
        funcs={"performance":performance,"portfolio":portfolio,"vectorbt":vector_screen,"jev_evaluate":evaluate,"prediction_archive":archive,"prediction_evaluate":evaluate_predictions}
        if job["kind"]=="rd_agent":value=rd_agent(job,data,output,root)
        else:value=funcs[job["kind"]](job,data,output)
        result["result"]=value;result["status"]="succeeded"
        if job["kind"] == "rd_agent" and value.get("state") and value["state"] != "succeeded":result["status"]="timed_out" if value["state"] == "timed_out" else "failed"
    except Unavailable as exc:
        result["status"]="unavailable";result["reason"]=str(exc)
    except Invalid as exc:
        result["status"]="rejected";result["reason"]=str(exc)
    except Exception as exc:
        result["status"]="failed";result["reason"]="adapter_failure:"+type(exc).__name__
    result["duration_ms"]=round((time.monotonic()-started)*1000)
    for artifact in sorted(output.rglob("*")):
        if artifact.is_symlink() or any(p.is_symlink() for p in artifact.parents if p != output and output in p.parents) or not artifact.is_file():continue
        size=artifact.stat().st_size
        if size>64*1024*1024:
            result["status"]="failed";result["reason"]="artifact_size_budget_exceeded";continue
        result["artifacts"].append({"path":artifact.relative_to(output).as_posix(),"sha256":sha(artifact.read_bytes()),"bytes":size})
    result["artifact_hash"]=digest(result["artifacts"])
    temporary=output/"result.pending.json";temporary.write_bytes(canonical(result));temporary.replace(output/"result.json")
    return result,output/"result.json"


def main():
    import argparse
    parser=argparse.ArgumentParser();parser.add_argument("--job",required=True,type=Path);parser.add_argument("--output",required=True,type=Path);args=parser.parse_args()
    try:
        result,path=run(args.job,args.output)
        print(json.dumps({"status":result["status"],"result_path":str(path)},ensure_ascii=False))
        return 0 if result["status"]=="succeeded" else 2
    except (Invalid,ValueError,OSError) as exc:
        print(json.dumps({"status":"rejected","reason":type(exc).__name__+":"+str(exc)},ensure_ascii=False));return 2
