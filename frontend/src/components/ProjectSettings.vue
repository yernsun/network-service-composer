<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { projectLifecycle as api } from '@project-lifecycle'
import type { DeletionPreview, LifecycleProject, ProjectDeletion } from '../data/project-lifecycle'
import { useWorkspaceStore } from '../stores/workspace'
import { errorText, ApiRequestError } from '../shared/api/client'
import { translate as t } from '../i18n'
import StatusBadge from './StatusBadge.vue'

const store = useWorkspaceStore(), router = useRouter()
const project = ref<LifecycleProject>(), preview = ref<DeletionPreview>(), task = ref<ProjectDeletion>()
const form = reactive({ name: '', description: '', default_runtime_id: '' })
const busy = ref(false), error = ref(''), force = ref(false), confirmation = ref(''), leaving = ref(false)
const dialog = ref<HTMLElement>(), previousFocus = ref<HTMLElement>()
let baseline = '', generation = 0, pollTimer: ReturnType<typeof setTimeout> | undefined
const writable = computed(() => api.writable() && !project.value?.system)
const dirty = computed(() => !!project.value && JSON.stringify(form) !== baseline)
const active = computed(() => task.value && ['QUEUED', 'RUNNING'].includes(task.value.status))
const resources = computed(() => preview.value?.resources.filter(r => !['pool', 'nginx-active', 'nginx-draft'].includes(r.kind)) || [])
const canSubmit = computed(() => writable.value && !busy.value && !dirty.value && !!preview.value && !preview.value.blockers.length && confirmation.value === preview.value.project_slug)
function setProject(p: LifecycleProject) {
  project.value = p
  Object.assign(form, { name: p.name, description: p.description || '', default_runtime_id: p.default_runtime_id || '' })
  baseline = JSON.stringify(form)
}
async function attempt(work: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await work() } catch (e) {
    error.value = errorText(e)
    if (!project.value) store.notify(error.value, true)
    if (e instanceof ApiRequestError) {
      const fields: Record<string, string> = { '/name': 'project-settings-name', '/description': 'project-settings-description', '/default_runtime_id': 'project-settings-runtime', '/confirm_slug': 'project-delete-confirm' }
      const id = fields[e.fields[0]?.pointer || '']
      if (id) { await nextTick(); document.getElementById(id)?.focus() }
    }
  } finally { busy.value = false }
}
async function completed() {
  await router.replace('/projects')
  await api.refresh()
  close(true)
  store.notify(t('projectLifecycle.deleted'), false)
}
function poll() {
  clearTimeout(pollTimer)
  if (!task.value || !active.value) return
  const id = task.value.id, token = generation
  pollTimer = setTimeout(async () => {
    try {
      const current = await api.task(id)
      if (token !== generation) return
      task.value = current
      if (current.status === 'SUCCEEDED') await completed()
      else poll()
    } catch (e) { if (token === generation) error.value = errorText(e) }
  }, 1200)
}
async function open(id: string) {
  if (project.value || busy.value) return
  previousFocus.value = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
  generation++; preview.value = undefined; task.value = undefined; force.value = false; confirmation.value = ''; leaving.value = false
  await attempt(async () => {
    setProject(await api.project(id))
    if (project.value?.deletion_id) {
      task.value = await api.task(project.value.deletion_id)
      force.value = task.value.preview.force
      poll()
    }
    await nextTick(); dialog.value?.querySelector<HTMLElement>('input,button')?.focus()
  })
}
function close(discard = false) {
  if (busy.value && !discard) return
  if (dirty.value && !discard) { leaving.value = true; return }
  generation++; clearTimeout(pollTimer); project.value = undefined; preview.value = undefined; task.value = undefined; leaving.value = false
  previousFocus.value?.focus()
}
async function save() {
  if (!project.value || !writable.value) return
  const p = project.value
  await attempt(async () => {
    setProject(await api.save(p.id, { ...form }, p.revision)); preview.value = undefined
    await api.refresh(); store.notify(t('integration.saved'), false)
  })
}
async function reviewDeletion() {
  if (!project.value || !writable.value || dirty.value) return
  await attempt(async () => { preview.value = await api.preview(project.value!.id, force.value); confirmation.value = '' })
}
async function submitDeletion() {
  if (!canSubmit.value || !preview.value) return
  await attempt(async () => {
    task.value = task.value ? await api.resume(task.value, preview.value!, confirmation.value) : await api.remove(preview.value!, confirmation.value)
    preview.value = undefined
    if (task.value.status === 'SUCCEEDED') await completed()
    else { await api.refresh(); poll() }
  })
}
async function refreshTask() {
  if (!task.value) return
  await attempt(async () => { task.value = await api.task(task.value!.id); if (task.value.status === 'SUCCEEDED') await completed(); else poll() })
}
function onClick(event: MouseEvent) {
  const button = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-action="p-project-settings"]') : null
  if (!button?.dataset.id) return
  event.preventDefault(); event.stopImmediatePropagation(); void open(button.dataset.id)
}
function onKey(event: KeyboardEvent) {
  if (!project.value) return
  event.stopImmediatePropagation()
  if (event.key === 'Escape') { event.preventDefault(); close(); return }
  if (event.key !== 'Tab') return
  const fields = [...(dialog.value?.querySelectorAll<HTMLElement>('button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled])') || [])].filter(el => el.getClientRects().length)
  const first = fields[0], last = fields.at(-1)
  if ((event.shiftKey && document.activeElement === first) || (!event.shiftKey && document.activeElement === last)) { event.preventDefault(); (event.shiftKey ? last : first)?.focus() }
}
function beforeUnload(event: BeforeUnloadEvent) { if (dirty.value) { event.preventDefault(); event.returnValue = '' } }
onMounted(() => { window.addEventListener('click', onClick, true); window.addEventListener('keydown', onKey, true); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { generation++; clearTimeout(pollTimer); window.removeEventListener('click', onClick, true); window.removeEventListener('keydown', onKey, true); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <div v-if="project" class="backdrop" @click.self="close()">
    <section ref="dialog" class="modal wide" role="dialog" aria-modal="true" aria-labelledby="project-settings-title" data-testid="project-settings-dialog">
      <div class="modal-head">
        <div><h2 id="project-settings-title">{{ t('projectLifecycle.settings') }}</h2><p>{{ project.name }} · {{ project.slug }}</p></div>
        <button class="iconbtn" :aria-label="t('common.close')" :disabled="busy" @click="close()">×</button>
      </div>
      <div class="modal-body">
        <p v-if="error" class="callout red" role="alert">{{ error }}</p>
        <template v-if="leaving">
          <p class="callout amber">{{ t('projectLifecycle.discardPrompt') }}</p>
          <button class="btn" @click="leaving = false">{{ t('projectLifecycle.keepEditing') }}</button>
          <button class="btn danger" @click="close(true)">{{ t('projectLifecycle.discard') }}</button>
        </template>
        <template v-else>
          <p v-if="project.system" class="callout blue">{{ t('problem.system.project_protected') }}</p>
          <form v-if="!task" @submit.prevent="save">
            <div class="field"><label class="field-label" for="project-settings-name">{{ t('projectLifecycle.name') }}</label><input id="project-settings-name" v-model="form.name" :disabled="busy || !writable" required maxlength="255"></div>
            <div class="field"><label class="field-label" for="project-settings-description">{{ t('projectLifecycle.description') }}</label><textarea id="project-settings-description" v-model="form.description" :disabled="busy || !writable" rows="3" maxlength="4096" /></div>
            <div class="field"><label class="field-label" for="project-settings-runtime">{{ t('projectLifecycle.runtime') }}</label><select id="project-settings-runtime" v-model="form.default_runtime_id" :disabled="busy || !writable"><option value="">{{ t('projectLifecycle.noDefault') }}</option><option v-for="runtime in store.state().runtimes.filter(r => r.canWrite || r.id === project?.default_runtime_id)" :key="runtime.id" :value="runtime.id">{{ runtime.name }}</option></select><p class="hint">{{ t('projectLifecycle.runtimeHint') }}</p></div>
            <button class="btn primary" type="submit" :disabled="busy || !writable || !dirty">{{ t('projectLifecycle.save') }}</button>
          </form>
          <template v-if="task">
            <h3>{{ t('projectLifecycle.progress') }}</h3>
            <p role="status"><StatusBadge :status="task.status" :label="t('projectLifecycle.status.' + task.status)" context="operation" /></p>
            <p v-if="task.error_code" class="callout amber">{{ t('problem.' + task.error_code) }}</p>
            <ul>
              <li v-for="app in task.preview.applications" :key="app.id">
                {{ app.name }} · <StatusBadge :status="app.status" :label="t('projectLifecycle.status.' + app.status)" context="operation" />
                <ul>
                  <li v-for="object in app.observation.objects" :key="object.id">
                    {{ object.service }} · <code>{{ object.id }}</code> · <StatusBadge :status="app.status === 'SUCCEEDED' || app.completed_objects?.includes(object.id) ? 'SUCCEEDED' : 'PENDING'" :label="t('projectLifecycle.status.' + (app.status === 'SUCCEEDED' || app.completed_objects?.includes(object.id) ? 'SUCCEEDED' : 'PENDING'))" context="operation" />
                  </li>
                </ul>
              </li>
            </ul>
            <button class="btn sm" :disabled="busy" @click="refreshTask">{{ t('projectLifecycle.refresh') }}</button>
          </template>
          <template v-if="!active">
            <hr>
            <h3>{{ t('projectLifecycle.delete') }}</h3>
            <p class="callout amber">{{ t('projectLifecycle.preserve') }}</p>
            <p v-if="dirty" class="hint">{{ t('projectLifecycle.saveFirst') }}</p>
            <label class="option-line"><input v-model="force" type="checkbox" :disabled="busy || !writable || !!task" @change="preview = undefined">{{ t('projectLifecycle.force') }}</label>
            <button class="btn danger" :disabled="busy || !writable || dirty" @click="reviewDeletion">{{ t('projectLifecycle.preview') }}</button>
          </template>
          <template v-if="preview">
            <h3>{{ t('projectLifecycle.resources') }}</h3>
            <ul v-if="resources.length"><li v-for="resource in resources" :key="resource.id">{{ resource.name }}<span v-if="resource.environment_id" class="muted"> · {{ store.state().environments.find(e => e.id === resource.environment_id)?.name }}</span></li></ul>
            <p v-else>{{ t('projectLifecycle.empty') }}</p>
            <div v-for="pool in preview.pools" :key="pool.id" class="callout blue">{{ t('projectLifecycle.poolScope', { name: pool.name, count: pool.gateway_ids.length }) }}</div>
            <p v-for="blocker in preview.blockers" :key="blocker.code + blocker.resource_id" class="callout red" role="alert">{{ t('problem.' + blocker.code) }} · {{ preview.resources.find(r => r.id === blocker.resource_id)?.name || blocker.resource_id }}</p>
            <div class="field"><label class="field-label" for="project-delete-confirm">{{ t('projectLifecycle.confirm', { slug: preview.project_slug }) }}</label><input id="project-delete-confirm" v-model="confirmation" :disabled="busy || !writable" autocomplete="off"></div>
          </template>
        </template>
      </div>
      <div v-if="!leaving" class="modal-foot">
        <button class="btn" :disabled="busy" @click="close()">{{ t('common.close') }}</button>
        <button v-if="preview" class="btn danger" :disabled="!canSubmit" @click="submitDeletion">{{ t(task ? 'projectLifecycle.resume' : 'projectLifecycle.delete') }}</button>
      </div>
    </section>
  </div>
</template>
