#!/usr/bin/env python3
"""Small loopback HTTP read benchmark, NOT a production capacity claim."""
import concurrent.futures as cf
import datetime as dt
import json
import os
import platform
import statistics
import time
import urllib.request
BASE=os.getenv('BEACON_URL','http://localhost:8080')
def request(_):
    start=time.perf_counter()
    try:
        with urllib.request.urlopen(BASE+'/v1/incidents?limit=50',timeout=15) as r:
            r.read();ok=r.status==200
    except Exception:ok=False
    return (time.perf_counter()-start)*1000,ok
results=[]
for concurrency in [10,50,100]:
    for i in range(10):request(i)
    started=time.perf_counter()
    with cf.ThreadPoolExecutor(max_workers=concurrency) as pool:values=list(pool.map(request,range(300)))
    durations=sorted(v[0] for v in values)
    results.append(dict(concurrency=concurrency,requests=300,p50_ms=round(statistics.median(durations),2),p95_ms=round(durations[int(len(durations)*.95)-1],2),errors=sum(not v[1] for v in values),elapsed_seconds=round(time.perf_counter()-started,2)))
print(json.dumps(dict(timestamp=dt.datetime.now(dt.timezone.utc).isoformat(),platform=platform.platform(),workload='GET /v1/incidents?limit=50; loopback; 300 reads per concurrency; warm cache; Python client and Go API share host',results=results),indent=2))
