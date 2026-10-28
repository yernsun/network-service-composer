#!/usr/bin/env python3
"""Notify NSC after pushing images. Secrets are read from the environment."""
import hashlib
import hmac
import json
import os
import time
import urllib.request
import uuid

body = json.dumps({"services": os.environ.get("NSC_IMAGE_SERVICES", "").split()}, separators=(",", ":")).encode()
timestamp = str(int(time.time()))
event = os.environ.get("NSC_WEBHOOK_EVENT_ID") or str(uuid.uuid4())
message = timestamp.encode() + b"." + event.encode() + b"." + body
signature = hmac.new(os.environ["NSC_WEBHOOK_SECRET"].encode(), message, hashlib.sha256).hexdigest()
request = urllib.request.Request(os.environ["NSC_WEBHOOK_URL"], data=body, method="POST", headers={
    "Content-Type": "application/json", "X-NSC-Timestamp": timestamp,
    "X-NSC-Event-ID": event, "X-NSC-Signature": "sha256=" + signature,
})
with urllib.request.urlopen(request, timeout=15) as response:
    print("Image check accepted:", response.status)
