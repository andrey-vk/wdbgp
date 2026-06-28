import AppLayout from '@/admin/layout/AppLayout.vue';
import { createRouter, createWebHistory } from 'vue-router';
import { useAuthStore } from '@/admin/stores/auth';

const router = createRouter({
    history: createWebHistory('/admin/'),
    routes: [
        {
            path: '/',
            component: AppLayout,
            children: [
                {
                    path: '/',
                    name: 'dashboard',
                    component: () => import('@/admin/views/Dashboard.vue')
                },
                {
                    path: '/users',
                    name: 'users',
                    component: () => import('@/admin/views/UsersPage.vue')
                },
                {
                    path: '/feeds',
                    name: 'feeds',
                    component: () => import('@/admin/views/FeedsPage.vue')
                },
                {
                    path: '/adapters',
                    name: 'adapters',
                    component: () => import('@/admin/views/AdaptersPage.vue')
                },
                {
                    path: '/modes',
                    name: 'modes',
                    component: () => import('@/admin/views/ModesPage.vue')
                },
                {
                    path: '/users/:id/selection',
                    name: 'userSelections',
                    component: () => import('@/admin/views/UserSelectionsPage.vue')
                },
                {
                    path: '/modes/:id/communities',
                    name: 'communities',
                    component: () => import('@/admin/views/CommunitiesPage.vue')
                },
                {
                    path: '/settings',
                    name: 'settings',
                    component: () => import('@/admin/views/SettingsPage.vue')
                },
                {
                    path: '/debug',
                    name: 'debug',
                    component: () => import('@/admin/views/DebugPage.vue')
                }
            ]
        },
        {
            path: '/landing',
            name: 'landing',
            meta: { public: true },
            component: () => import('@/admin/views/pages/Landing.vue')
        },
        {
            path: '/pages/notfound',
            name: 'notfound',
            meta: { public: true },
            component: () => import('@/admin/views/pages/NotFound.vue')
        },
        {
            path: '/auth/login',
            name: 'login',
            meta: { public: true },
            component: () => import('@/admin/views/pages/auth/Login.vue')
        },
        {
            path: '/auth/access',
            name: 'accessDenied',
            meta: { public: true },
            component: () => import('@/admin/views/pages/auth/Access.vue')
        },
        {
            path: '/auth/error',
            name: 'error',
            meta: { public: true },
            component: () => import('@/admin/views/pages/auth/Error.vue')
        }
    ]
});

// Navigation guard: redirect unauthenticated users to login
router.beforeEach(async (to, _from, next) => {
  if (to.meta.public) {
    next()
    return
  }

  const authStore = useAuthStore()
  await authStore.checkAuth()

  if (authStore.isAuthenticated) {
    next()
  } else {
    next({ name: 'login' })
  }
})

export default router;
