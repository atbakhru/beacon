#!/usr/bin/env python3
"""Exercise real HTTP -> outbox -> Kafka -> correlation -> worker -> pgvector."""
import json,time,uuid
from seed import call
start=time.monotonic();key=str(uuid.uuid4());service=call('/v1/services')['items'][0]
a={'source_event_id':key,'service_id':service['id'],'signature':'smoke-'+key,'severity':'critical','summary':'Database connection pool exhausted'}
first=call('/v1/alerts',a);second=call('/v1/alerts',a)
assert first['id']==second['id'] and second['duplicate']
for _ in range(180):
    result=call('/v1/incidents?limit=500')['items']
    matches=[i for i in result if i['signature']==a['signature']]
    if matches:break
    time.sleep(.5)
else:raise AssertionError('Kafka correlation timeout')
i=matches[0];assert i['alert_count']==1
for action,state in [('ack','acknowledged'),('resolve','resolved'),('reopen','open')]:assert call(f"/v1/incidents/{i['id']}/{action}",{})['state']==state
for _ in range(60):
    result=call('/v1/search/incidents',{'query':'Database connection pool exhausted'})
    if result['items']:break
    time.sleep(.5)
assert result['items'] and 'pool' in result['items'][0]['incident']['title'].lower()
events=call(f"/v1/incidents/{i['id']}/timeline")['items'];assert len(events)>=4
print(json.dumps({'status':'passed','checks':['durable ingest','duplicate idempotency','Kafka correlation','lifecycle','durable timeline','worker encoding','pgvector ranking'],'elapsed_seconds':round(time.monotonic()-start,2),'model':result['model']},indent=2))
