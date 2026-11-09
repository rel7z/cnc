#!/usr/bin/env python3
"""Verify form login, private API access, bad credentials and logout."""
import http.cookiejar,json,sys,urllib.request,urllib.error
base=sys.argv[1] if len(sys.argv)>1 else 'http://172.236.141.170'
jar=http.cookiejar.CookieJar()
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def request(path,body=None,origin=None):
    req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers={'Origin':origin or base,'Content-Type':'application/json'})
    try:
        with opener.open(req,timeout=30) as r:return r.status,r.read(),r.headers
    except urllib.error.HTTPError as e:return e.code,e.read(),e.headers
status,page,_=request('/login');assert status==200 and (b'Welcome back.' in page or b'Selamat datang kembali.' in page)
with opener.open(base+'/dashboard',timeout=30) as response:
    assert response.geturl().startswith(base+'/login'),response.geturl()
assert request('/api/workers')[0]==401
assert request('/api/auth/login',{'username':'fahrel','password':'invalid'})[0]==401
assert request('/api/auth/login',{'username':'fahrel','password':'fahrel'},'http://other.example')[0]==403
status,body,headers=request('/api/auth/login',{'username':'fahrel','password':'fahrel'})
assert status==200,(status,body)
cookie=headers.get('Set-Cookie','');assert 'HttpOnly' in cookie and 'SameSite=lax' in cookie
assert request('/api/workers')[0]==200
assert request('/dashboard')[0]==200
status,body,_=request('/api/auth/logout',{});assert status==200,(status,body)
assert request('/api/workers')[0]==401
print(json.dumps({'login_page':'verified','default_credentials':'verified','wrong_password':'rejected','cross_origin_login':'rejected','session_cookie':'HttpOnly; SameSite=Lax','protected_dashboard_and_api':'verified','logout':'verified'}))
