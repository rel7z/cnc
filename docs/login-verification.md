# Login UI verification

Production release: `6420a0b57a489d8bd5458c17d6d05edf645f5c60`.

The new `/login` page replaces Nginx Basic Auth following explicit user approval. The requested default account is fahrel / fahrel. Runtime credentials and session key persist outside Git in `/opt/cnc/login.env`, protected by mode 0600.

Verified locally and in production: successful default login, incorrect-password rejection, cross-origin login rejection, HttpOnly/SameSite session cookie, protected dashboard and API, and logout. Unit tests cover signature tampering, expiration, and invalidation after credential rotation. TypeScript and production build passed.

Browser inspection confirmed desktop and narrow layouts, error feedback, successful login, live dashboard updates, the processor panel, six registered workers, and logout returning to the login page. All six workers also completed a harmless smoke task through the authenticated production API.

Public URL: http://172.236.141.170/login
