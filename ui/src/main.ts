import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './style.css'
import { routes } from './router'

const router = createRouter({
  history: createWebHistory('/ui/'),
  routes,
})

createApp(App).use(router).mount('#app')
