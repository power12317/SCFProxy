# -*- coding: utf8 -*-
import json
import os
from base64 import b64decode, b64encode

import urllib3
urllib3.disable_warnings()


def handler(event: dict, context: dict):
    # 验证暗号
    expected_secret = os.environ.get('SCF_SECRET_KEY', '')
    if expected_secret:
        headers = event.get('headers', {})
        client_secret = headers.get('X-Scf-Secret-Key', '')
        if client_secret != expected_secret:
            return {
                "statusCode": 403,
                "headers": {},
                "body": json.dumps({"error": "Forbidden: Invalid secret key"})
            }

    data = b64decode(event["body"]).decode()
    kwargs = json.loads(data)
    kwargs['body'] = b64decode(kwargs['body'])

    http = urllib3.PoolManager(cert_reqs="CERT_NONE")

    r = http.request(**kwargs, retries=False, decode_content=False)

    # 处理 headers，保留多个相同名称的 header（如多个 set-cookie）
    headers = {}
    for key in r.headers:
        # 获取该 key 的所有值（可能是多个）
        values = r.headers.getlist(key)
        if len(values) == 1:
            # 只有一个值，直接存储（key 转小写，value 保持原样）
            headers[key.lower()] = values[0]
        else:
            # 有多个值（如 set-cookie），存储为数组（key 转小写，value 保持原样）
            headers[key.lower()] = values

    response = {
        "headers": headers,
        "status_code": r.status,
        "content": b64encode(r._body).decode('utf-8')
    }

    return response