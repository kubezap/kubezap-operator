import type { RouteRecordRaw } from 'vue-router'

export const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: _to => {
      const ns = localStorage.getItem('kubezap-namespace') || 'default'
      return `/runs/${ns}`
    },
  },
  {
    path: '/runs/:namespace',
    component: () => import('../views/FlowRunList.vue'),
    props: true,
  },
  {
    path: '/runs/:namespace/:name',
    component: () => import('../views/FlowRunDetail.vue'),
    props: true,
  },
  {
    path: '/triggers/:namespace',
    component: () => import('../views/TriggerList.vue'),
    props: true,
  },
  {
    path: '/flows/:namespace',
    component: () => import('../views/FlowList.vue'),
    props: true,
  },
]
