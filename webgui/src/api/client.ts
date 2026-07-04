import axios from 'axios'

const apiClient = axios.create({
  baseURL: '/api',
  withCredentials: true,
  // Double-submit CSRF protection: the backend pairs the session cookie
  // with a non-HttpOnly wdbgp_csrf cookie the browser can read; axios
  // echoes its value back as X-CSRF-Token on every request.
  xsrfCookieName: 'wdbgp_csrf',
  xsrfHeaderName: 'X-CSRF-Token',
  withXSRFToken: true,
  headers: {
    'Content-Type': 'application/json',
  },
})

// These endpoints' own 401 responses are expected and already handled by
// their caller (login-form validation, the session-probe itself) — the
// blanket redirect below must not hijack them.
const ADMIN_AUTH_ENDPOINTS = ['/admin/login', '/admin/me', '/admin/logout']

// This client is shared by both the admin and user SPAs (separate Vite
// entries — see vite.config.ts), so the redirect only applies to /admin/
// calls; a plain user's session expiring must not send them to the admin
// login page. Uses a hard navigation rather than the admin router so this
// low-level shared module doesn't have to import (and leak into the user
// bundle) admin-only router/store code.
apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    const url = axios.isAxiosError(error) ? error.config?.url : undefined
    if (
      axios.isAxiosError(error) &&
      error.response?.status === 401 &&
      typeof url === 'string' &&
      url.startsWith('/admin/') &&
      !ADMIN_AUTH_ENDPOINTS.includes(url) &&
      !window.location.pathname.endsWith('/auth/login')
    ) {
      window.location.href = '/admin/auth/login'
    }
    return Promise.reject(error)
  },
)

export default apiClient
