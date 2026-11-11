#!/usr/bin/env python3
"""Deterministic, idempotent 1,200-alert fixture across six services."""
import datetime as dt
import json
import os
import time
import urllib.request
BASE=os.getenv('BEACON_URL','http://localhost:8080')
def call(path,body=None):
    req=urllib.request.Request(BASE+path,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json'})
    with urllib.request.urlopen(req,timeout=30) as r:return json.load(r)
def seed():
    services={s['name']:s['id'] for s in call('/v1/services')['items']}
    patterns=[('checkout-api','connection-pool','critical','Database connection pool exhausted'),('payment-service','upstream-timeout','high','Payment authorization latency elevated'),('auth-service','token-errors','high','Token verification failures increasing'),('notification-worker','queue-lag','medium','Notification queue processing delayed'),('inventory-api','cache-miss','medium','Inventory cache hit rate degraded'),('api-gateway','5xx-rate','critical','Upstream 5xx error rate elevated')]
    now=dt.datetime.now(dt.timezone.utc);alerts=[]
    for group in range(20):
        service,signature,severity,title=patterns[group%6]
        for j in range(60):
            at=now-dt.timedelta(hours=group+1) if group>=5 else now-dt.timedelta(minutes=group*2+2)
            alerts.append(dict(source_event_id=f'seed-v1-{group}-{j}',service_id=services[service],signature=signature,severity=severity,summary=title,seen_at=(at+dt.timedelta(seconds=j)).isoformat()))
    for offset in range(0,len(alerts),300):
        r=call('/v1/alerts/batch',{'alerts':alerts[offset:offset+300]})
        assert not any('error' in i for i in r['items']),r
    for _ in range(120):
        if call('/v1/stats')['pending']==0:break
        time.sleep(1)
    else:raise RuntimeError('Correlation did not drain within 120 seconds')
    for i in call('/v1/incidents?limit=500')['items']:
        if dt.datetime.fromisoformat(i['opened_at'].replace('Z','+00:00')) < now-dt.timedelta(hours=1) and i['state']!='resolved':call(f"/v1/incidents/{i['id']}/resolve",{})
    print(json.dumps({'seeded':1200,'stats':call('/v1/stats')},indent=2))
if __name__=='__main__':seed()
