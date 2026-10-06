<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch, type Directive } from 'vue'

type Kind = 'lost' | 'found'
type Revision = {
  item_name: string; description: string; category?: string | null; location?: string | null; event_time?: string | null
  private_verification_detail?: string | null
  moderation_status: string; rejection_reason?: string | null
}
type Post = {
  id: number; kind: Kind; item_name: string; description: string; category?: string; location?: string
  event_time?: string; lifecycle_status: string; moderation_status: string; rejection_reason?: string
  created_at: string; approved_at?: string; withdrawn?: boolean; author_nickname?: string; author_student_number?: string
  freshness_status?: string; private_verification_detail?: string | null; requires_claim_verification?: boolean
  match_reasons?: string[]
  pending_revision?: Revision | null; latest_revision?: Revision | null
}
type RequestItem = {
  id: number; post_id: number; kind: 'claim' | 'lead'; explanation: string; contact_method: string
  status: string; resolution_reason?: string; item_name?: string; requester_nickname?: string
  verification_answer?: string | null
}
type Profile = { id: number; username: string; nickname: string; role: string; student_number: string; student_number_verified: boolean }
type Report = { id: number; target_type: string; target_id: number; explanation: string; status: string; moderator_reason?: string; target?: Record<string, unknown> }

const page = ref<'browse' | 'mine' | 'requests' | 'admin'>('browse')
const accessToken = ref('')
const profile = ref<Profile | null>(null)
const posts = ref<Post[]>([])
const isPostsLoading = ref(true)
const hasLoadedPosts = ref(false)
const postsLoadFailed = ref(false)
const mine = ref<Post[]>([])
const requests = ref<RequestItem[]>([])
const selected = ref<Post | null>(null)
const ownerRequests = ref<RequestItem[]>([])
const recommendations = ref<Post[]>([])
const reviews = ref<{ posts: Post[]; revisions: Record<string, unknown>[] }>({ posts: [], revisions: [] })
const reports = ref<Report[]>([])
const query = reactive({ q: '', kind: '', status: '', location: '' })
const accountMode = ref<'login' | 'register'>('login')
const accountOpen = ref(false)
const publishOpen = ref(false)
const notice = ref('')
const error = ref('')
const toastKey = ref(0)
let toastDismissTimer: ReturnType<typeof setTimeout> | undefined
const busy = ref(false)
const total = ref(0)
const offset = ref(0)
const form = reactive({ username: '', password: '', nickname: '', student_number: '' })
const postForm = reactive({ kind: 'lost' as Kind, item_name: '', description: '', category: '', location: '', event_time: '', private_verification_detail: '' })
const requestForm = reactive({ explanation: '', contact_method: '', verification_answer: '' })
const reason = ref('')
const reportTarget = ref<{ type: 'post' | 'request'; id: number } | null>(null)
const heroIntroEnabled = ref(true)
const isPostsUpdating = computed(() => isPostsLoading.value && hasLoadedPosts.value)

type RevealOptions = { key?: string | number; delay?: number }
const revealedMotionKeys = new Set<string>()
let revealObserver: IntersectionObserver | null = null
const vRevealOnce: Directive<HTMLElement, RevealOptions | undefined> = {
  mounted(element, binding) {
    const key = binding.value?.key === undefined ? undefined : String(binding.value.key)
    element.classList.add('motion-reveal')
    if (key) element.dataset.revealKey = key
    if (binding.value?.delay) element.style.setProperty('--motion-delay', `${binding.value.delay}ms`)
    if ((key && revealedMotionKeys.has(key)) || typeof IntersectionObserver === 'undefined') {
      element.classList.add('motion-revealed')
      return
    }
    revealObserver ??= new IntersectionObserver((entries, observer) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        const revealedKey = (entry.target as HTMLElement).dataset.revealKey
        if (revealedKey) revealedMotionKeys.add(revealedKey)
        entry.target.classList.add('motion-revealed')
        observer.unobserve(entry.target)
      }
    }, { threshold: 0.12, rootMargin: '0px 0px -24px 0px' })
    revealObserver.observe(element)
  },
  unmounted(element) {
    revealObserver?.unobserve(element)
  },
}

const isAdmin = computed(() => profile.value?.role === 'admin')
const humanKind = (kind: Kind | string) => kind === 'lost' ? '寻物启事' : kind === 'found' ? '拾获公告' : kind === 'claim' ? '认领申请' : '线索'
const lifecycle = (post: Post) => post.lifecycle_status
const dateText = (value?: string) => value ? new Date(value).toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' }) : '时间待补充'
let postsRequestId = 0

let refreshPromise: Promise<string | null> | null = null
async function refreshAccessToken(): Promise<string | null> {
  if (!refreshPromise) {
    refreshPromise = (async () => {
      try {
        const response = await fetch('/api/auth/refresh', { method: 'POST', credentials: 'include' })
        if (!response.ok) return null
        const payload = await response.json()
        return typeof payload.access_token === 'string' ? payload.access_token : null
      } catch { return null }
      finally { refreshPromise = null }
    })()
  }
  return refreshPromise
}

async function api<T = any>(path: string, options: RequestInit = {}, canRefresh = true): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if (accessToken.value && !path.endsWith('/auth/refresh')) headers.set('Authorization', `Bearer ${accessToken.value}`)
  const response = await fetch(`/api${path}`, {
    ...options,
    credentials: 'include',
    headers,
  })
  if (response.status === 401 && canRefresh && path !== '/auth/refresh' && path !== '/auth/login' && path !== '/auth/register') {
    const token = await refreshAccessToken()
    if (token) {
      accessToken.value = token
      return api<T>(path, options, false)
    }
    accessToken.value = ''
    profile.value = null
  }
  if (!response.ok) {
    const payload = await response.json().catch(() => ({}))
    throw new Error(typeof payload.detail === 'string' ? payload.detail : '请求未能完成，请检查填写内容。')
  }
  return response.status === 204 ? undefined as T : response.json()
}
const send = (method: string, body?: unknown): RequestInit => ({ method, ...(body === undefined ? {} : { body: JSON.stringify(body) }) })

async function loadPosts() {
  const requestId = ++postsRequestId
  const params = new URLSearchParams({ limit: '12', offset: String(offset.value) })
  if (query.q.trim()) params.set('q', query.q.trim())
  if (query.kind) params.set('kind', query.kind)
  if (query.status) params.set('status', query.status)
  if (query.location.trim()) params.set('location', query.location.trim())
  isPostsLoading.value = true
  postsLoadFailed.value = false
  try {
    const result = await api<{ items: Post[]; total: number }>(`/posts?${params}`)
    if (requestId !== postsRequestId) return
    posts.value = result.items
    total.value = result.total
    hasLoadedPosts.value = true
  } catch (caught) {
    if (requestId !== postsRequestId) return
    if (!hasLoadedPosts.value) postsLoadFailed.value = true
    throw caught
  } finally {
    if (requestId === postsRequestId) isPostsLoading.value = false
  }
}
function refreshPosts() {
  loadPosts().catch((caught) => { error.value = (caught as Error).message })
}
function searchPosts() { offset.value = 0; refreshPosts() }
function clearFilters() {
  const filterWatcherWillReload = Boolean(query.kind || query.status)
  query.q = ''; query.kind = ''; query.status = ''; query.location = ''
  offset.value = 0
  if (!filterWatcherWillReload) refreshPosts()
}
function previousPostsPage() { offset.value = Math.max(0, offset.value - 12); refreshPosts() }
function nextPostsPage() { offset.value += 12; refreshPosts() }
async function loadMine() { mine.value = await api<Post[]>('/my/posts') }
async function loadRequests() { requests.value = await api<RequestItem[]>('/my/requests') }
async function loadAdmin() {
  const [nextReviews, nextReports] = await Promise.all([api('/admin/reviews'), api<Report[]>('/admin/reports')])
  reviews.value = nextReviews; reports.value = nextReports
}
async function refresh() {
  error.value = ''
  try {
    if (profile.value && page.value === 'browse') await loadMine()
    if (page.value === 'browse') await loadPosts()
    if (page.value === 'mine') await loadMine()
    if (page.value === 'requests') await loadRequests()
    if (page.value === 'admin' && isAdmin.value) await loadAdmin()
  } catch (caught) { error.value = (caught as Error).message }
}
async function refreshProfile() {
  try {
    const token = await refreshAccessToken()
    if (!token) { accessToken.value = ''; profile.value = null; return }
    accessToken.value = token
    profile.value = await api<Profile>('/auth/me')
  } catch { accessToken.value = ''; profile.value = null }
}
watch(page, refresh)
watch([notice, error], ([nextNotice, nextError]) => {
  if (toastDismissTimer) clearTimeout(toastDismissTimer)
  if (!nextNotice && !nextError) return
  toastKey.value += 1
  toastDismissTimer = setTimeout(dismissToast, 5000)
})
watch([page, selected], ([nextPage, nextSelected]) => {
  if (nextPage !== 'browse' || nextSelected) heroIntroEnabled.value = false
})
watch(() => [query.kind, query.status], () => { offset.value = 0; if (page.value === 'browse') refreshPosts() })
onMounted(async () => { await refreshProfile(); await refresh() })
onUnmounted(() => { if (toastDismissTimer) clearTimeout(toastDismissTimer) })

function dismissToast() {
  if (toastDismissTimer) clearTimeout(toastDismissTimer)
  toastDismissTimer = undefined
  notice.value = ''
  error.value = ''
}

async function accountSubmit() {
  busy.value = true; error.value = ''
  try {
    const payload = accountMode.value === 'register'
      ? { username: form.username, password: form.password, nickname: form.nickname, student_number: form.student_number }
      : { username: form.username, password: form.password }
    const result = await api<{ access_token: string; user: Profile }>('/auth/' + (accountMode.value === 'register' ? 'register' : 'login'), send('POST', payload))
    accessToken.value = result.access_token
    profile.value = result.user
    accountOpen.value = false; notice.value = '欢迎回来，' + profile.value?.nickname; await refresh()
  } catch (caught) { error.value = (caught as Error).message } finally { busy.value = false }
}
async function signOut() { try { await api('/auth/logout', send('POST')) } finally { accessToken.value = ''; profile.value = null }; selected.value = null; notice.value = '已安全退出'; page.value = 'browse'; await refresh() }
async function createPost() {
  try {
    await api('/posts', send('POST', postPayload()))
    publishOpen.value = false; notice.value = '已提交审核，通过后会出现在广场。'; await loadMine()
  } catch (caught) { error.value = (caught as Error).message }
}
async function openPost(post: Post) {
  try {
    const owned = mine.value.find((candidate) => candidate.id === post.id)
    selected.value = owned || await api<Post>(`/posts/${post.id}`)
    ownerRequests.value = []
    recommendations.value = []
    requestForm.verification_answer = ''; requestForm.explanation = ''; requestForm.contact_method = ''
    recommendations.value = (await api<{ items: Post[] }>(`/posts/${post.id}/recommendations`)).items
    if (profile.value?.id && mine.value.some((owned) => owned.id === post.id)) ownerRequests.value = await api<RequestItem[]>(`/posts/${post.id}/requests`)
  } catch (caught) { error.value = (caught as Error).message }
}
async function submitRequest() {
  if (!selected.value) return
  try {
    await api(`/posts/${selected.value.id}/requests`, send('POST', requestForm))
    requestForm.explanation = ''; requestForm.contact_method = ''; requestForm.verification_answer = ''; notice.value = '已私密发送给发布者。'; await loadRequests()
  } catch (caught) { error.value = (caught as Error).message }
}
async function mutatePost(path: string, body: unknown, success: string) {
  try { await api(path, send('POST', body)); notice.value = success; await refresh(); if (selected.value) await openPost(selected.value) }
  catch (caught) { error.value = (caught as Error).message }
}
async function handleRequest(id: number, action: 'accept' | 'reject') {
  const body = action === 'reject' ? { reason: reason.value || '暂不符合处理条件' } : undefined
  await mutatePost(`/requests/${id}/${action}`, body, action === 'accept' ? '已接受，相关内容状态已更新。' : '已拒绝该请求。')
  reason.value = ''
}
async function deletePost(post: Post) {
  try { await api(`/posts/${post.id}`, { method: 'DELETE' }); selected.value = null; notice.value = '内容已删除或撤下。'; await loadMine() }
  catch (caught) { error.value = (caught as Error).message }
}
async function withdrawRequest(id: number) { await mutatePost(`/requests/${id}/withdraw`, {}, '请求已撤回。') }
async function confirmPost(post: Post) {
  try { await api(`/posts/${post.id}/confirm`, send('POST', {})); notice.value = '已确认信息仍然有效。'; await loadMine(); if (selected.value?.id === post.id) await openPost(post) }
  catch (caught) { error.value = (caught as Error).message }
}
async function updatePost(post: Post) {
  try {
    await api(`/posts/${post.id}`, { method: 'PUT', body: JSON.stringify(postPayload()), headers: { 'Content-Type': 'application/json' } })
    publishOpen.value = false; notice.value = '内容已提交审核。'; await loadMine(); await openPost(post)
  } catch (caught) { error.value = (caught as Error).message }
}
async function sendReport() {
  if (!reportTarget.value) return
  try {
    await api(`/${reportTarget.value.type === 'post' ? 'posts' : 'requests'}/${reportTarget.value.id}/reports`, send('POST', { explanation: reason.value }))
    notice.value = '举报已提交，管理员会按流程处理。'; reportTarget.value = null; reason.value = ''; await refresh()
  } catch (caught) { error.value = (caught as Error).message }
}
async function adminDecision(path: string, body: unknown, message: string) {
  try { await api(path, send('POST', body)); notice.value = message; reason.value = ''; await loadAdmin() }
  catch (caught) { error.value = (caught as Error).message }
}
async function takeDownSelected() {
  if (!selected.value) return
  await adminDecision(`/admin/posts/${selected.value.id}/take-down`, { reason: reason.value || '经核查确认需要下架。' }, '内容已下架，处理记录已保存。')
  if (!error.value) { selected.value = null; page.value = 'browse'; await loadPosts() }
}
function editPost(post: Post) {
  const draft = post.pending_revision || (post.latest_revision?.moderation_status === '已驳回' ? post.latest_revision : post)
  const privateDetail = Object.prototype.hasOwnProperty.call(draft, 'private_verification_detail') ? draft.private_verification_detail : post.private_verification_detail
  Object.assign(postForm, { kind: post.kind, item_name: draft.item_name, description: draft.description, category: draft.category || '', location: draft.location || '', event_time: draft.event_time || '', private_verification_detail: privateDetail || '' })
  selected.value = post; publishOpen.value = true
}
function postPayload() { return { ...postForm, category: postForm.category || null, location: postForm.location || null, event_time: postForm.event_time || null, private_verification_detail: postForm.kind === 'found' ? (postForm.private_verification_detail.trim() || null) : null } }
</script>

<template>
  <div class="app-shell">
    <header class="topbar">
      <a class="brand" href="#" @click.prevent="page = 'browse'; selected = null">
        <span class="brand-mark">拾</span><span><b>拾光</b><small>HDU LOST & FOUND</small></span>
      </a>
      <nav class="main-nav" aria-label="主导航">
        <button :class="{ active: page === 'browse' }" @click="page = 'browse'; selected = null">失物广场</button>
        <button v-if="profile" :class="{ active: page === 'mine' }" @click="page = 'mine'; selected = null">我的发布</button>
        <button v-if="profile" :class="{ active: page === 'requests' }" @click="page = 'requests'; selected = null">我的申请</button>
        <button v-if="isAdmin" :class="{ active: page === 'admin' }" @click="page = 'admin'; selected = null">管理工作台 <i v-if="reviews.posts.length + reviews.revisions.length" class="nav-dot" /></button>
      </nav>
      <div class="account-actions">
        <template v-if="profile"><span class="avatar">{{ profile.nickname.slice(0, 1) }}</span><span class="user-name">{{ profile.nickname }}</span><small class="student-mini">学号 {{ profile.student_number }}</small><button class="quiet-button" @click="signOut">退出</button></template>
        <button v-else class="login-button" @click="accountOpen = true">登录 / 注册 <span>↗</span></button>
      </div>
    </header>

    <main>
      <section v-if="page === 'browse' && !selected" class="hero" :class="{ 'hero--intro': heroIntroEnabled }">
        <div class="hero-copy">
          <div class="eyebrow"><span class="eyebrow-line" /> 杭州电子科技大学 · 校园互助</div>
          <h1>让每件遗失物<br /><em>都能回家。</em></h1>
          <p>一点线索，也许就是重逢的开始。<br />在校园里，帮彼此找回重要的小事。</p>
          <button class="hero-cta" @click="profile ? publishOpen = true : accountOpen = true">发布一条信息 <span>＋</span></button>
        </div>
        <div class="hero-art" aria-hidden="true">
          <div class="orbit orbit-one" /><div class="orbit orbit-two" />
          <div class="sun-disc" />
          <div class="campus-card"><span>校园寻物手记</span><strong>拾物<br />有归期</strong><small>NO. 026 · 2026</small></div>
          <div class="floating-note note-a">今日新增 <b>{{ total }}</b> 条</div>
          <div class="floating-note note-b"><span>✳</span> 让善意抵达</div>
          <div class="hero-stamp">HDU<br /><small>HELP</small></div>
        </div>
        <div class="hero-foot"><span class="hero-caption">从一条线索开始，慢慢找回来</span><span class="hero-scroll">向下浏览 ↓</span></div>
      </section>

      <section v-if="page === 'browse' && !selected" class="board-section">
        <div class="section-head" v-reveal-once="{ key: 'board-heading' }"><div><div class="eyebrow muted"><span class="eyebrow-line" /> 信息板 · {{ total }} 条公开信息</div><h2>最近的校园线索</h2></div><div class="list-actions"><span v-if="isPostsUpdating" class="update-status" role="status" aria-live="polite">正在更新</span><button class="text-link" :disabled="isPostsUpdating" @click="refreshPosts">更新列表 <span>↻</span></button></div></div>
        <div class="filter-bar" v-reveal-once="{ key: 'board-filters', delay: 70 }">
          <label class="search-field"><span>⌕</span><input v-model="query.q" placeholder="搜索物品名称或描述" @keydown.enter="searchPosts" /><button v-if="query.q" aria-label="清除搜索" @click="query.q = ''; searchPosts()">×</button></label>
          <select v-model="query.kind" aria-label="内容类型"><option value="">全部信息</option><option value="lost">寻物启事</option><option value="found">拾获公告</option></select>
          <select v-model="query.status" aria-label="处理状态"><option value="">所有状态</option><option>寻找中</option><option>待认领</option><option>已找回</option><option>已归还</option><option>已结束</option></select>
          <input v-model="query.location" class="location-filter" placeholder="地点" @keydown.enter="searchPosts" />
          <button class="filter-go" @click="searchPosts">查找 <span>→</span></button>
        </div>
        <div class="board-results" :aria-busy="isPostsLoading">
          <p v-if="isPostsLoading && !hasLoadedPosts" class="sr-only" role="status">正在加载公开信息</p>
          <div v-if="isPostsLoading && !hasLoadedPosts" class="post-grid skeleton-grid" aria-hidden="true">
            <div v-for="slot in 4" :key="slot" class="post-card post-skeleton"><div class="skeleton-top"><i /><i /></div><div class="skeleton-visual" /><i class="skeleton-title" /><i class="skeleton-line" /><div class="skeleton-bottom"><i /><i /></div></div>
          </div>
          <div v-else-if="postsLoadFailed && !hasLoadedPosts" class="empty-state load-error" role="alert"><span class="empty-orbit">!</span><h3>线索暂时加载失败</h3><p>请检查网络后重试。加载失败不会被当作没有搜索结果。</p><button @click="refreshPosts">重试</button></div>
          <div v-else-if="posts.length" class="post-grid">
            <button v-for="(post, index) in posts" :key="post.id" v-reveal-once="{ key: post.id, delay: Math.min(130 + index * 22, 360) }" class="post-card" :class="['tone-' + (index % 4)]" @click="openPost(post)">
              <div class="card-top"><span class="kind-label" :class="post.kind">{{ humanKind(post.kind) }}</span><span class="card-date">{{ dateText(post.approved_at || post.created_at) }}</span></div>
              <div class="item-illustration" :class="'illustration-' + (index % 4)"><span>{{ ['✳', '◌', '⌑', '✦'][index % 4] }}</span><i>{{ post.category || '校园物件' }}</i></div>
              <h3>{{ post.item_name }}</h3><p>{{ post.description }}</p>
              <div class="card-meta"><span>⌖ {{ post.location || '地点待补充' }}</span><span class="status-chip">{{ lifecycle(post) }}</span><span v-if="post.freshness_status === '待确认'" class="freshness-chip">待确认</span></div>
              <div class="card-bottom"><span>{{ post.author_nickname || '校园同学' }} 发布</span><span class="arrow-round">↗</span></div>
            </button>
          </div>
          <div v-else class="empty-state" v-reveal-once><span class="empty-orbit">⌕</span><h3>还没有找到相关线索</h3><p>换一个关键词，或者发一条寻物信息，让大家一起留意。</p><button @click="clearFilters">清除筛选</button></div>
          <div v-if="hasLoadedPosts && total > 12" class="pagination"><button :disabled="offset === 0" @click="previousPostsPage">← 上一页</button><span>{{ offset + 1 }}–{{ Math.min(offset + 12, total) }} / {{ total }}</span><button :disabled="offset + 12 >= total" @click="nextPostsPage">下一页 →</button></div>
        </div>
      </section>

      <section v-if="selected" class="detail-section">
        <button class="back-link" @click="selected = null">← 返回信息板</button>
        <div class="detail-layout"><article class="detail-main"><span class="kind-label" :class="selected.kind">{{ humanKind(selected.kind) }}</span><div class="detail-visual"><span>{{ selected.kind === 'lost' ? '⌕' : '✳' }}</span><small>{{ selected.category || '校园物件' }} · {{ selected.location || '地点待补充' }}</small></div><div class="eyebrow muted">{{ selected.lifecycle_status }} · {{ selected.event_time || dateText(selected.created_at) }}<span v-if="selected.freshness_status === '待确认'"> · 待确认仍然有效</span></div><h1>{{ selected.item_name }}</h1><p class="detail-description">{{ selected.description }}</p><div v-if="profile && mine.some((owned) => owned.id === selected?.id) && selected.private_verification_detail" class="private-detail"><small>作者私密核验特征</small><b>{{ selected.private_verification_detail }}</b><span>仅你可见，公开页面不会显示。</span></div><div class="detail-author"><span class="avatar">{{ selected.author_nickname?.slice(0, 1) || '同' }}</span><div><b>{{ selected.author_nickname || '校园同学' }}</b><small>已通过管理员审核 · 联系方式仅在申请后私下沟通</small></div></div>
          <section v-if="profile && mine.some((owned) => owned.id === selected?.id)" class="owner-panel"><div class="panel-title"><h3>收到的申请</h3><span>{{ ownerRequests.filter((r) => r.status === '待处理').length }} 项待处理</span></div><div v-if="ownerRequests.length" class="request-list"><div v-for="req in ownerRequests" :key="req.id" class="request-card"><div class="request-top"><b>{{ humanKind(req.kind) }} · {{ req.requester_nickname }}</b><span class="status-chip">{{ req.status }}</span></div><p>{{ req.explanation }}</p><p v-if="req.verification_answer" class="verification-answer">私密核验回答：{{ req.verification_answer }}</p><div class="contact-line">私密联系方式：{{ req.contact_method }}</div><div class="inline-actions"><button v-if="req.status === '待处理'" class="small-primary" @click="handleRequest(req.id, 'accept')">接受申请</button><button v-if="req.status === '待处理'" class="small-quiet" @click="handleRequest(req.id, 'reject')">拒绝</button><button class="small-quiet" @click="reportTarget = { type: 'request', id: req.id }">举报此请求</button></div></div></div><p v-else class="subtle">暂无申请。新的申请会显示在这里。</p></section>
        </article>
        <aside class="detail-aside"><div class="aside-status"><small>当前进度</small><strong>{{ selected.lifecycle_status }}</strong><span>审核状态 · {{ selected.moderation_status }}</span></div>
          <template v-if="profile && !isAdmin && !mine.some((owned) => owned.id === selected?.id) && ['寻找中','待认领'].includes(selected.lifecycle_status)"><h3>{{ selected.kind === 'lost' ? '你有相关线索吗？' : '这是你的物品吗？' }}</h3><p>申请内容与联系方式只会发送给发布者，不会公开显示。</p><form class="stack-form" @submit.prevent="submitRequest"><label>说说情况<textarea v-model="requestForm.explanation" required minlength="5" placeholder="描述你看到的情况，或说说物品特征…" /></label><label v-if="selected.kind === 'found' && selected.requires_claim_verification">请描述一项物品特征<input v-model="requestForm.verification_answer" required maxlength="500" placeholder="回答发布者设置的私密核验问题" /></label><label>方便联系你的方式<input v-model="requestForm.contact_method" required placeholder="微信号、手机号或邮箱" /></label><button class="primary-action">{{ selected.kind === 'lost' ? '发送线索' : '提交认领' }} <span>→</span></button></form></template>
          <div v-else-if="!profile" class="signin-nudge"><span>✳</span><h3>让这条线索继续</h3><p>登录后就能私下联系发布者，或帮助失主找回物品。</p><button class="primary-action" @click="accountOpen = true">登录后参与 <span>→</span></button></div>
          <div v-if="profile && !mine.some((owned) => owned.id === selected?.id)" class="report-link"><button @click="reportTarget = { type: 'post', id: selected!.id }">举报此内容</button><small>发现不当信息？告诉管理员。</small></div>
          <div v-if="isAdmin && !mine.some((owned) => owned.id === selected?.id)" class="owner-panel admin-takedown"><h3>管理员操作</h3><p>下架后内容会从公开广场隐藏，并保留相关处理历史。</p><label>下架原因<input v-model="reason" placeholder="请说明处理依据" /></label><button class="small-danger" @click="takeDownSelected">下架此内容</button></div>
          <div v-if="profile && mine.some((owned) => owned.id === selected?.id)" class="stack-form owner-tools"><p v-if="selected.freshness_status === '待确认'" class="freshness-reminder">这条信息已满 30 天未确认，仍会公开展示。请确认它是否依然有效。</p><button v-if="selected.freshness_status === '待确认'" class="small-primary" @click="confirmPost(selected!)">确认仍然有效</button><label>更新处理状态<select :value="selected.lifecycle_status" @change="mutatePost(`/posts/${selected!.id}/status`, { status: ($event.target as HTMLSelectElement).value }, '处理状态已更新。')"><option v-if="selected.kind === 'lost'">寻找中</option><option v-if="selected.kind === 'found'">待认领</option><option>{{ selected.kind === 'lost' ? '已找回' : '已归还' }}</option><option>已结束</option></select></label><button class="outline-action" @click="editPost(selected!)">编辑内容 / 重新提交</button><button class="small-quiet" @click="deletePost(selected!)">删除或撤下内容</button></div>
        </aside></div>
        <section v-if="recommendations.length" class="recommendation-section"><div class="eyebrow muted"><span class="eyebrow-line" /> 相反类型的相关线索</div><h2>也许能帮上忙。</h2><div class="recommendation-grid"><button v-for="post in recommendations" :key="post.id" class="recommendation-card" @click="openPost(post)"><span class="kind-label" :class="post.kind">{{ humanKind(post.kind) }}</span><b>{{ post.item_name }}</b><small>{{ post.location || '地点待补充' }} · {{ post.event_time || dateText(post.created_at) }}</small><span class="match-reasons">匹配依据：{{ post.match_reasons?.join('、') }}</span></button></div></section>
      </section>

      <section v-if="page === 'mine' && !selected" class="workspace-section"><div class="section-head"><div><div class="eyebrow muted"><span class="eyebrow-line" /> 个人空间</div><h2>我的发布</h2></div><button class="primary-action compact" @click="publishOpen = true">发布新信息 <span>＋</span></button></div><div v-if="mine.length" class="own-list"><article v-for="post in mine" :key="post.id" class="own-row"><div class="own-type" :class="post.kind">{{ post.kind === 'lost' ? '寻物' : '拾获' }}</div><div class="own-info"><button class="own-title" @click="openPost(post)">{{ post.item_name }}</button><p>{{ post.description }}</p><small>{{ dateText(post.created_at) }} · {{ post.location || '地点待补充' }}</small></div><div class="own-state"><span class="status-chip" :class="post.withdrawn ? 'rejected' : post.moderation_status === '已驳回' ? 'rejected' : ''">{{ post.withdrawn ? '已撤下' : post.moderation_status }}</span><small v-if="post.freshness_status === '待确认'" class="freshness-reminder">公开信息已满 30 天，待你确认</small><button v-if="post.freshness_status === '待确认'" class="small-primary" @click="confirmPost(post)">确认仍然有效</button><small v-if="post.moderation_status === '已驳回'">{{ post.rejection_reason }}</small><small v-if="post.pending_revision">修改审核中，公开版本暂保持不变</small><small v-if="post.latest_revision?.moderation_status === '已驳回'" class="reason-note">修改被驳回：{{ post.latest_revision.rejection_reason }}</small></div><button class="arrow-round" @click="openPost(post)">↗</button></article></div><div v-else class="empty-state"><span class="empty-orbit">＋</span><h3>还没有发布内容</h3><p>发布寻物启事或拾获公告，让校园里的善意流动起来。</p><button @click="publishOpen = true">发布第一条</button></div><div class="identity-note"><span>ⓘ</span> 学号只用于本地账号标识，当前未接入学校统一认证，其他用户看不到你的学号。</div></section>

      <section v-if="page === 'requests' && !selected" class="workspace-section"><div class="section-head"><div><div class="eyebrow muted"><span class="eyebrow-line" /> 跟进进展</div><h2>我的申请</h2></div></div><div v-if="requests.length" class="own-list"><article v-for="req in requests" :key="req.id" class="own-row"><div class="own-type" :class="req.kind">{{ req.kind === 'claim' ? '认领' : '线索' }}</div><div class="own-info"><b>{{ req.item_name }}</b><p>{{ req.explanation }}</p><small v-if="req.verification_answer">我的私密核验回答：{{ req.verification_answer }}</small><small>我的联系方式：{{ req.contact_method }}</small><small v-if="req.resolution_reason" class="reason-note">{{ req.resolution_reason }}</small></div><div class="own-state"><span class="status-chip">{{ req.status }}</span><button v-if="req.status === '待处理'" class="small-quiet" @click="withdrawRequest(req.id)">撤回申请</button><button class="small-quiet" @click="reportTarget = { type: 'request', id: req.id }">举报此请求</button></div></article></div><div v-else class="empty-state"><span class="empty-orbit">↗</span><h3>申请记录会出现在这里</h3><p>找到相关物品后提交线索或认领，发布者的处理进度也会同步更新。</p><button @click="page = 'browse'">去信息广场</button></div></section>

      <section v-if="page === 'admin' && !selected && isAdmin" class="workspace-section admin-section"><div class="section-head"><div><div class="eyebrow muted"><span class="eyebrow-line" /> 平台治理 · 管理员专属</div><h2>管理工作台</h2></div><span class="admin-seal">MODERATION DESK · HDU</span></div><div class="admin-columns"><section class="admin-column"><div class="panel-title"><h3>待审核内容 <span>{{ reviews.posts.length + reviews.revisions.length }}</span></h3></div><div v-if="!reviews.posts.length && !reviews.revisions.length" class="admin-empty">当前没有待审核内容。</div><article v-for="post in reviews.posts" :key="'p'+post.id" class="moderation-card"><span class="kind-label" :class="post.kind">{{ humanKind(post.kind) }}</span><h4>{{ post.item_name }}</h4><p>{{ post.description }}</p><small>{{ post.author_nickname }} · 学号 {{ post.author_student_number }} · {{ dateText(post.created_at) }}</small><div class="moderation-actions"><button class="small-primary" @click="adminDecision(`/admin/reviews/${post.id}/approve`, undefined, '内容已审核通过。')">通过</button><button class="small-danger" @click="adminDecision(`/admin/reviews/${post.id}/reject`, { reason: reason || '内容不符合平台规范，请修改后重新提交。' }, '已退回并通知发布者。')">驳回</button></div><input v-model="reason" class="reason-input" placeholder="驳回原因（至少 3 字）" /></article><article v-for="revision in reviews.revisions" :key="'r'+revision.post_id" class="moderation-card"><span class="kind-label found">内容修改</span><h4>{{ revision.item_name }}</h4><p>{{ revision.description }}</p><small>发布者 {{ revision.author_nickname }} · 学号 {{ revision.author_student_number }} · 原公开信息保留至本次审核通过</small><div class="moderation-actions"><button class="small-primary" @click="adminDecision(`/admin/reviews/${revision.post_id}/approve`, undefined, '修改已审核通过并更新公开版本。')">通过并更新</button><button class="small-danger" @click="adminDecision(`/admin/reviews/${revision.post_id}/reject`, { reason: reason || '修改内容不符合平台规范，请调整后重新提交。' }, '修改已退回，原公开版本保持不变。')">驳回修改</button></div><input v-model="reason" class="reason-input" placeholder="驳回原因（至少 3 字）" /></article></section>
        <section class="admin-column"><div class="panel-title"><h3>举报队列 <span>{{ reports.filter((r) => r.status === '待处理').length }}</span></h3></div><div v-if="!reports.length" class="admin-empty">当前没有待处理举报。</div><article v-for="report in reports" :key="report.id" class="moderation-card report-card"><div class="report-heading"><span class="kind-label">{{ report.target_type === 'post' ? '公开内容' : '私密请求' }}</span><span class="status-chip">{{ report.status }}</span></div><p>举报原因：{{ report.explanation }}</p><div v-if="report.target" class="reported-target"><b>{{ report.target.item_name || '相关私密请求' }}</b><p>{{ report.target.description || report.target.explanation }}</p><small v-if="report.target.contact_method">参与者联系方式：{{ report.target.contact_method }}</small></div><small>举报编号 #{{ report.id }}</small><div v-if="report.status === '待处理'" class="moderation-actions"><button class="small-quiet" @click="adminDecision(`/admin/reports/${report.id}/dismiss`, { reason: reason || '经核查暂不处理。' }, '举报已结案。')">驳回举报</button><button class="small-danger" @click="adminDecision(`/admin/reports/${report.id}/remove`, { reason: reason || '经核查确认违规，已移除。' }, '已移除举报目标并保留处理记录。')">移除目标</button></div><input v-if="report.status === '待处理'" v-model="reason" class="reason-input" placeholder="处理说明（至少 3 字）" /></article></section></div><div class="identity-note"><span>ⓘ</span> 私密认领和线索默认不会进入管理员列表；只有参与者举报后，相关内容才对管理员开放查看。</div></section>

      <section v-if="page === 'browse' && !selected" class="closing-note"><span class="closing-mark">拾</span><div><b>一件小事，一个温柔的校园。</b><small>拾光 · 为每一份善意留个位置</small></div><span class="closing-right">HANGZHOU DIANZI UNIVERSITY<br />LOST & FOUND · 2026</span></section>
    </main>

    <Transition name="toast">
      <div v-if="notice || error" :key="toastKey" class="toast" :class="error ? 'toast-error' : ''" :role="error ? 'alert' : 'status'">
        <span>{{ error || notice }}</span><button aria-label="关闭提示" @click="dismissToast">×</button>
      </div>
    </Transition>

    <div v-if="accountOpen" class="modal-backdrop" @click.self="accountOpen = false"><section class="dialog account-dialog"><button class="dialog-close" aria-label="关闭" @click="accountOpen = false">×</button><div class="eyebrow muted"><span class="eyebrow-line" /> {{ accountMode === 'login' ? '欢迎回来' : '加入校园互助' }}</div><h2>{{ accountMode === 'login' ? '继续拾光。' : '让善意有个入口。' }}</h2><p class="dialog-intro">登录后可以发布信息、提交线索，并私下联系物品的发布者。</p><form class="stack-form" @submit.prevent="accountSubmit"><label>用户名<input v-model="form.username" required autocomplete="username" placeholder="2–32 位字母、数字或符号" /></label><label>密码<input v-model="form.password" type="password" required minlength="10" autocomplete="current-password" placeholder="至少 10 位" /></label><template v-if="accountMode === 'register'"><label>称呼<input v-model="form.nickname" required maxlength="40" placeholder="大家怎么称呼你" /></label><label>学号<input v-model="form.student_number" required minlength="4" placeholder="仅用于本地账号标识" /></label><small class="form-note">当前未连接学校统一身份认证，学号尚未由学校核验。</small></template><button class="primary-action" :disabled="busy">{{ busy ? '请稍候…' : accountMode === 'login' ? '登录' : '创建账号' }} <span>→</span></button></form><button class="switch-mode" @click="accountMode = accountMode === 'login' ? 'register' : 'login'">{{ accountMode === 'login' ? '还没有账号？创建一个' : '已有账号？返回登录' }}</button></section></div>

    <div v-if="publishOpen" class="modal-backdrop" @click.self="publishOpen = false"><section class="dialog publish-dialog"><button class="dialog-close" aria-label="关闭" @click="publishOpen = false">×</button><div class="eyebrow muted"><span class="eyebrow-line" /> 信息发布</div><h2>{{ selected && mine.some((p) => p.id === selected?.id) ? '更新物品信息' : '写下一条线索。' }}</h2><p class="dialog-intro">内容会先由管理员审核；修改已通过的内容时，旧版本会继续公开，直至新版本审核通过。</p><form class="stack-form" @submit.prevent="selected && mine.some((p) => p.id === selected?.id) ? updatePost(selected!) : createPost()"><div class="type-switch"><button type="button" :class="{ chosen: postForm.kind === 'lost' }" @click="postForm.kind = 'lost'">我丢失了</button><button type="button" :class="{ chosen: postForm.kind === 'found' }" @click="postForm.kind = 'found'">我捡到了</button></div><label>物品名称<input v-model="postForm.item_name" required maxlength="100" placeholder="例如：黑色折叠伞" /></label><label>描述<textarea v-model="postForm.description" required minlength="5" maxlength="3000" placeholder="尽量描述外观、特征或发现时的情况…" /></label><label v-if="postForm.kind === 'found'">私密核验特征（选填）<textarea v-model="postForm.private_verification_detail" maxlength="300" placeholder="填写一项公开描述中没有的物品特征，仅你和相关认领流程可见" /><small class="form-note">填写后，认领人需要单独回答；答案不会公开显示。</small></label><div class="form-row"><label>物品类别<input v-model="postForm.category" placeholder="例如：数码 / 文具" /></label><label>大概地点<input v-model="postForm.location" placeholder="例如：下沙校区图书馆" /></label></div><label>丢失 / 捡到时间<input v-model="postForm.event_time" placeholder="例如：10 月 3 日下午" /></label><button class="primary-action">提交管理员审核 <span>→</span></button></form></section></div>

    <div v-if="reportTarget" class="modal-backdrop" @click.self="reportTarget = null"><section class="dialog"><button class="dialog-close" aria-label="关闭" @click="reportTarget = null">×</button><div class="eyebrow muted"><span class="eyebrow-line" /> 内容治理</div><h2>报告一项问题。</h2><form class="stack-form" @submit.prevent="sendReport"><label>情况说明<textarea v-model="reason" required minlength="5" placeholder="请描述你认为需要管理员核查的情况。" /></label><p class="form-note">举报会进入管理员处理记录，仅用于内容审核。</p><button class="primary-action">提交举报 <span>→</span></button></form></section></div>
  </div>
</template>
