#!/usr/bin/env python3
import json
import subprocess
import sys
import time
import urllib.parse
import urllib.request

def send_email(subject, body):
    cmd = [
        "docker", "exec", "-i", "itsm-postgres-dev",
        "psql", "-U", "itsm_base_system_20260908", "-d", "itsm_config_baseline_20260908",
        "-t", "-A", "-c",
        "SELECT credentials, settings FROM connector_configs WHERE id = 1;"
    ]
    out = subprocess.check_output(cmd).decode().strip()
    parts = out.split('|')
    creds = json.loads(parts[0]) if parts[0].startswith('{') else dict(x.split('=', 1) for x in parts[0].splitlines() if '=' in x)
    settings = json.loads(parts[1]) if parts[1].startswith('{') else dict(x.split('=', 1) for x in parts[1].splitlines() if '=' in x)
    client_id = creds.get('azure_client_id')
    client_secret = creds.get('azure_client_secret')
    tenant_id = settings.get('azure_tenant_id')

    # Get Azure AD token
    token_url = f"https://login.microsoftonline.com/{tenant_id}/oauth2/v2.0/token"
    data = urllib.parse.urlencode({
        'client_id': client_id,
        'client_secret': client_secret,
        'scope': 'https://graph.microsoft.com/.default',
        'grant_type': 'client_credentials'
    }).encode()
    req = urllib.request.Request(token_url, data=data, headers={'Content-Type': 'application/x-www-form-urlencoded'})
    with urllib.request.urlopen(req) as resp:
        token = json.loads(resp.read().decode())['access_token']

    # Send email from Julian@dawnpro.onmicrosoft.com to ai-support@dawnpro.onmicrosoft.com
    send_url = 'https://graph.microsoft.com/v1.0/users/Julian@dawnpro.onmicrosoft.com/sendMail'
    mail_payload = {
        'message': {
            'subject': subject,
            'body': {
                'contentType': 'Text',
                'content': body
            },
            'toRecipients': [
                {'emailAddress': {'address': 'ai-support@dawnpro.onmicrosoft.com'}}
            ]
        },
        'saveToSentItems': 'true'
    }
    req_send = urllib.request.Request(send_url, data=json.dumps(mail_payload).encode('utf-8'), headers={
        'Authorization': f'Bearer {token}',
        'Content-Type': 'application/json'
    })
    with urllib.request.urlopen(req_send) as resp_send:
        print(f"Graph sendMail accepted: status {resp_send.status}")
        return resp_send.status

if __name__ == '__main__':
    subj = sys.argv[1] if len(sys.argv) > 1 else f"[E2E-AUTO-{int(time.time())}] Luka 申请出差值班 SSLVPN 权限"
    body = sys.argv[2] if len(sys.argv) > 2 else "您好，我是Luka(王雅蓉)，因出差需要申请开通SSL-VPN远程办公权限，请协助审批并开通。"
    send_email(subj, body)
