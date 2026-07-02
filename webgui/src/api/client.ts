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

export default apiClient
