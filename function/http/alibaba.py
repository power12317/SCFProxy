# -*- coding: utf8 -*-
import json
import os
from base64 import b64decode, b64encode

import urllib3
urllib3.disable_warnings()


def handler(environ: dict, start_response):
    # 验证暗号
    expected_secret = os.environ.get('SCF_SECRET_KEY', '')
    if expected_secret:
        client_secret = environ.get('HTTP_X_SCF_SECRET_KEY', '')
        if client_secret != expected_secret:
            start_response('403 Forbidden', [('Content-type', 'text/plain')])
            return [b'Forbidden: Invalid secret key']

    try:
        request_body_size = int(environ.get('CONTENT_LENGTH', 0))
    except (ValueError):
        request_body_size = 0
    request_body = environ['wsgi.input'].read(request_body_size)

    kwargs = json.loads(request_body.decode("utf-8"))
    kwargs['body'] = b64decode(kwargs['body'])

    http = urllib3.PoolManager(cert_reqs="CERT_NONE")
    # Prohibit automatic redirect to avoid network errors such as connection reset
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

    status = '200 OK'
    response_headers = [('Content-type', 'text/json')]
    start_response(status, response_headers)
    return [json.dumps(response)]