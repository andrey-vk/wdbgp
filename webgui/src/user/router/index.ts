import { createRouter, createWebHistory } from 'vue-router'
import UserPage from '../views/UserPage.vue'

export default createRouter({
  history: createWebHistory('/'),
  routes: [
    { path: '/', name: 'home', component: UserPage },
    { path: '/:pathMatch(.*)*', redirect: { name: 'home' } },
  ],
})
