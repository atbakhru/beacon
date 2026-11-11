FROM python:3.12-slim
WORKDIR /app
ARG SEMANTIC=false
COPY requirements*.txt ./
RUN --mount=type=secret,id=proxy_ca,target=/usr/local/share/ca-certificates/session.crt sh -c 'if [ -f /usr/local/share/ca-certificates/session.crt ]; then update-ca-certificates; export PIP_CERT=/etc/ssl/certs/ca-certificates.crt; fi; if [ "$SEMANTIC" = true ]; then pip install --no-cache-dir -r requirements-semantic.txt; else pip install --no-cache-dir -r requirements.txt; fi'
COPY worker.py ./
RUN useradd -m worker
USER worker
EXPOSE 8091
CMD ["python", "worker.py"]
