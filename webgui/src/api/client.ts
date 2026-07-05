import axios from 'axios'

declare module 'axios' {
  export interface AxiosRequestConfig {
    // Opt out of the global 401-redirect interceptor below — for a request
    // whose caller already handles its own 401 as an expected response
    // (a session probe, a login attempt, a logout), not a session that died
    // out from under it. Set at the call site that knows this, rather than
    // listing exempt URLs here where a future self-handling endpoint could
    // easily be forgotten.
    skipAuthRedirect?: boolean
  }
}

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
      !error.config?.skipAuthRedirect &&
      typeof url === 'string' &&
      url.startsWith('/admin/') &&
      !window.location.pathname.endsWith('/auth/login')
    ) {
      window.location.href = '/admin/auth/login'
    }
    return Promise.reject(error)
  },
)

export default apiClient
