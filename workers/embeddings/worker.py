"""Versioned embeddings, Kafka updates, and a query encoder sharing one model.

The lightweight default is signed feature hashing, NOT a learned semantic model.
Install requirements-semantic.txt and set MODEL_NAME=sentence-transformers/all-MiniLM-L6-v2 to enable
384-dimensional TextEmbedding embeddings for both indexing and queries.
"""
import hashlib
import json
import logging
import math
import os
import re
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import psycopg
from kafka import KafkaConsumer

logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(message)s')
MODEL = os.getenv('MODEL_NAME', 'feature-hash-v1')
DB = os.environ['DATABASE_URL']
model = None
model_lock = threading.Lock()
if MODEL != 'feature-hash-v1':
    from fastembed import TextEmbedding
    model = TextEmbedding(model_name=MODEL, cache_dir=os.getenv("MODEL_CACHE", "/tmp/beacon-models"))



def embed(text):
    if model is not None:
        with model_lock:
            vector = next(model.embed([text])).tolist()
            if len(vector) != 384:
                raise ValueError("Model must output 384 dimensions")
            return vector
    vec = [0.0] * 384
    words = re.findall(r'[a-z0-9]+', text.lower())
    tokens = words + [a + '_' + b for a, b in zip(words, words[1:])]
    for token in tokens:
        digest = hashlib.sha256(token.encode()).digest()
        vec[int.from_bytes(digest[:4], 'big') % 384] += 1 if digest[4] & 1 else -1
    norm = math.sqrt(sum(x*x for x in vec)) or 1
    return [x/norm for x in vec]


def index_pending():
    # Backfill also repairs a crash or model-version change; compare incident versions
    # so an older computation can never overwrite a newer stored embedding.
    with psycopg.connect(DB) as conn:
        rows = conn.execute("""SELECT i.id,i.title,i.signature,i.version FROM incidents i
          LEFT JOIN incident_embeddings e ON e.incident_id=i.id
          WHERE e.incident_id IS NULL OR e.incident_version<i.version OR e.model_version<>%s
          ORDER BY i.updated_at LIMIT 100""", (MODEL,)).fetchall()
        for iid, title, signature, version in rows:
            vector = json.dumps(embed(title + ' ' + signature))
            conn.execute("""INSERT INTO incident_embeddings(incident_id,embedding,model_version,incident_version)
              VALUES(%s,%s::vector,%s,%s) ON CONFLICT(incident_id) DO UPDATE
              SET embedding=excluded.embedding,model_version=excluded.model_version,
              incident_version=excluded.incident_version,updated_at=now()
              WHERE incident_embeddings.incident_version<=excluded.incident_version""",
                         (iid, vector, MODEL, version))
        return len(rows)


def consume():
    while True:
        consumer = None
        try:
            consumer = KafkaConsumer('incidents.events.v1',
                bootstrap_servers=os.getenv('KAFKA_BROKERS', 'localhost:19092').split(','),
                group_id='beacon-embeddings-v1', enable_auto_commit=False,
                auto_offset_reset='earliest', consumer_timeout_ms=2000)
            while True:
                consumer.poll(timeout_ms=2000, max_records=200)
                while index_pending() == 100:
                    pass
                consumer.commit()
        except Exception:
            logging.exception('Embedding loop will retry')
            time.sleep(2)
        finally:
            if consumer:
                consumer.close()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != '/healthz':
            self.send_error(404); return
        self.respond({'status': 'ok', 'model': MODEL})

    def do_POST(self):
        if self.path != '/embed':
            self.send_error(404); return
        try:
            length = int(self.headers.get('Content-Length', '0'))
            if length < 1 or length > 8192:
                raise ValueError('Invalid length')
            value = json.loads(self.rfile.read(length))
            text = value['text']
            if not isinstance(text, str) or not text.strip() or len(text) > 2000:
                raise ValueError('Invalid text')
            self.respond({'vector': embed(text), 'model': MODEL})
        except (ValueError, KeyError, TypeError):
            self.send_error(400, 'Valid text required')

    def respond(self, value):
        data = json.dumps(value).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers(); self.wfile.write(data)

    def log_message(self, *args):
        pass


if __name__ == '__main__':
    threading.Thread(target=consume, daemon=True).start()
    ThreadingHTTPServer(('0.0.0.0', 8091), Handler).serve_forever()
