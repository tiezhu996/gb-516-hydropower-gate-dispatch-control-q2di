<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue';
import { Plus, Refresh, Search } from '@element-plus/icons-vue';
import type { DomainRecord, EntityConfig } from '../types/domain';
import { allowedTransitions } from '../types/status';
import { formatDate, riskLabel, statusLabel } from '../utils/format';
import { useAuth } from '../hooks/useAuth';
import { usePolling } from '../hooks/usePolling';
import { request } from '../api/client';
import StatusBadge from './common/StatusBadge.vue';
import GateStateBadge from './common/GateStateBadge.vue';
import MetricCard from './common/MetricCard.vue';
import DirectiveTimeline from './common/DirectiveTimeline.vue';
import ConfirmDialog from './common/ConfirmDialog.vue';

const props = defineProps<{ config: EntityConfig; store: any }>();
const { session, can } = useAuth();
const search = ref('');
const showCreate = ref(false);
const pending = ref<{ item: DomainRecord; status: string } | null>(null);
const transitionReason = ref('');
const relatedOptions = ref<DomainRecord[]>([]);
const createForm = reactive({
  code: '', name: '', description: '', facility: '', owner: '', category: '',
  riskLevel: 'medium', metricValue: 0, metricUnit: '%', evidence: '', relatedCode: '', gateState: 'closed',
  waterLevel: 0, waterLevelLower: null as number | null, waterLevelUpper: null as number | null,
});

const highRisk = computed(() => props.store.items.filter((item: DomainRecord) => ['high', 'critical'].includes(item.riskLevel)).length);
const canCreate = computed(() => can('operator', 'admin'));
const pageDescription = computed(() => ({
  reservoir: '监控水位阈值与许可窗口，为调度决策提供约束。',
  gateUnit: '查看闸门实时状态，所有开闭动作必须经过中间态。',
  operationDirective: '编排闸门指令，并由不同账号完成提交与安全复核。',
  executionConfirmation: '记录现场执行结果、证据及关联操作指令。',
}[props.config.key] || `管理${props.config.label}状态、风险与责任人。`));

async function load(): Promise<void> {
  await props.store.load(props.config.path, search.value);
}

onMounted(() => void load());
usePolling(load, 30_000);

const metricLabel = computed(() => ({
	reservoir: '当前水位', gateUnit: '当前开度', operationDirective: '目标开度', executionConfirmation: '实际开度',
}[props.config.key] || '指标值'));

const relationLabel = computed(() => ({ gateUnit: '所属库区', operationDirective: '目标闸门', executionConfirmation: '关联指令' }[props.config.key] || '关联对象'));

const createReady = computed(() => Boolean(
	createForm.code.trim() && createForm.name.trim() && createForm.facility.trim() && createForm.owner.trim() &&
	createForm.category.trim() && createForm.evidence.trim() &&
	(!['gateUnit', 'operationDirective', 'executionConfirmation'].includes(props.config.key) || createForm.relatedCode),
));

async function prepareCreate(): Promise<void> {
  const suffix = String(Date.now()).slice(-6);
  createForm.code = `${props.config.key.replace(/[a-z]/g, (value) => value.toUpperCase()).slice(0, 3)}-${suffix}`;
	createForm.name = '';
	createForm.description = '';
	createForm.facility = '';
  createForm.owner = session.value?.displayName || '现场操作员';
	createForm.category = '';
	createForm.metricValue = 0;
	createForm.metricUnit = props.config.key === 'reservoir' ? 'm' : '%';
	createForm.evidence = '';
	createForm.relatedCode = '';
  createForm.gateState = 'closed';
	createForm.waterLevel = 0;
	createForm.waterLevelLower = null;
	createForm.waterLevelUpper = null;
	relatedOptions.value = [];
	const relationPaths: Record<string, string> = { gateUnit: 'reservoirs', operationDirective: 'gates', executionConfirmation: 'directives' };
	try {
		const relationPath = relationPaths[props.config.key];
		if (relationPath) {
			const result = await request<DomainRecord[]>(`/${relationPath}?page=1&pageSize=100`);
			relatedOptions.value = result.data.filter((item) =>
				props.config.key === 'operationDirective' ? item.status !== 'locked' :
				props.config.key === 'executionConfirmation' ? item.status === 'executing' : true,
			);
			selectRelated(relatedOptions.value[0]?.code || '');
		}
		showCreate.value = true;
	} catch (reason) {
		props.store.error = reason instanceof Error ? reason.message : String(reason);
	}
}

function selectRelated(code: string): void {
	createForm.relatedCode = code;
	const related = relatedOptions.value.find((item) => item.code === code);
	if (related) createForm.facility = related.facility;
}

async function createRecord(): Promise<void> {
	if (!createReady.value) {
		props.store.error = '请完整填写必填业务字段和现场证据';
		return;
	}
	if (props.config.key === 'reservoir') {
		const hasLower = createForm.waterLevelLower !== null && createForm.waterLevelLower !== undefined;
		const hasUpper = createForm.waterLevelUpper !== null && createForm.waterLevelUpper !== undefined;
		if (hasLower !== hasUpper) {
			props.store.error = '水位许可区间的下限和上限必须同时填写或同时留空';
			return;
		}
		if (hasLower && hasUpper && (createForm.waterLevelLower as number) >= (createForm.waterLevelUpper as number)) {
			props.store.error = '水位许可区间下限必须小于上限（米）';
			return;
		}
	}
  await props.store.createRecord(props.config.path, buildCreatePayload());
  if (!props.store.error) showCreate.value = false;
}

function buildCreatePayload(): Record<string, unknown> {
	const payload: Record<string, unknown> = { ...createForm, effectiveAt: new Date().toISOString() };
	if (props.config.key !== 'reservoir') {
		delete payload.waterLevel;
		delete payload.waterLevelLower;
		delete payload.waterLevelUpper;
	}
	return payload;
}

// formatLevel 把水位数值统一显示为两位小数的米数；缺失时显示“-”。
function formatLevel(value: number | null | undefined): string {
	return value === null || value === undefined ? '-' : `${Number(value).toFixed(2)} 米`;
}

// rangeText 展示库区水位许可区间；没填区间时提示按现状放行。
function rangeText(row: DomainRecord): string {
	if (row.waterLevelLower === null || row.waterLevelLower === undefined ||
		row.waterLevelUpper === null || row.waterLevelUpper === undefined) {
		return '未设置区间';
	}
	return `${Number(row.waterLevelLower).toFixed(2)} ~ ${Number(row.waterLevelUpper).toFixed(2)} 米`;
}

// directivePermit 只对已复核（approved）的指令给出本次放行结论；其他状态
// 尚未走到“推进执行”这道闸，列表上不做放行判定。
function directivePermit(row: DomainRecord): { show: boolean; permitted: boolean; text: string } {
	if (row.status !== 'approved') return { show: false, permitted: true, text: '' };
	if (row.permitted) {
		return { show: true, permitted: true, text: row.hasWaterLevelRange ? `区间内，可放行（${rangeText(row)}）` : '库区未设区间，按现状放行' };
	}
	return { show: true, permitted: false, text: row.permitReason || '水位不满足放行区间' };
}

function transitionsFor(item: DomainRecord): readonly string[] {
  return allowedTransitions(props.config.key, item.status).filter((target) => {
    if (props.config.key !== 'operationDirective') return can('operator', 'admin');
	if (target === 'completed') return false;
    if (target === 'pending' || target === 'executing' || target === 'completed') return can('operator', 'admin');
    if (target === 'approved') return can('reviewer', 'admin') && item.submittedBy !== session.value?.username;
    if (target === 'aborted') return can('operator', 'reviewer', 'admin');
    return false;
  });
}

function selectTransition(item: DomainRecord, status: string): void {
  pending.value = { item, status };
  transitionReason.value = status === 'approved'
    ? '已复核闸门目标、水位窗口、设备闭锁和现场证据'
    : `值班人员确认将状态由 ${item.status} 推进至 ${status}`;
}

async function confirmTransition(): Promise<void> {
  if (!pending.value || transitionReason.value.trim().length < 3) return;
  await props.store.transition(props.config.path, pending.value.item, pending.value.status, transitionReason.value.trim());
  if (!props.store.error) pending.value = null;
}
</script>

<template>
  <main class="workspace">
    <header class="page-header">
      <div><p class="eyebrow">业务工作台</p><h1>{{ config.label }}</h1><p>{{ pageDescription }}</p></div>
      <el-button v-if="canCreate" type="primary" :icon="Plus" @click="prepareCreate">新增{{ config.label }}</el-button>
    </header>

    <section class="metrics" aria-label="业务统计">
      <MetricCard label="记录总数" :value="store.meta.total" detail="当前筛选范围" />
      <MetricCard label="高风险" :value="highRisk" detail="需要优先复核" />
      <MetricCard label="状态种类" :value="new Set(store.items.map((item: DomainRecord) => item.status)).size" detail="当前状态覆盖" />
    </section>

    <DirectiveTimeline
      v-if="['operationDirective', 'executionConfirmation'].includes(config.key)"
      :records="store.items"
      :kind="config.key"
    />

    <section class="toolbar" aria-label="筛选工具栏">
      <el-input v-model="search" :prefix-icon="Search" :placeholder="`搜索${config.label}编码或名称`" clearable @keyup.enter="load" />
      <el-button type="primary" :icon="Search" @click="load">查询</el-button>
      <el-button :icon="Refresh" @click="search = ''; load()">重置</el-button>
    </section>
    <el-alert v-if="store.error" :title="store.error" type="error" show-icon closable @close="store.error = ''" />

    <section class="table-shell">
      <el-table v-loading="store.loading" :data="store.items" empty-text="暂无符合条件的记录">
        <el-table-column prop="code" label="编码" width="130" />
        <el-table-column label="名称" min-width="190">
          <template #default="{ row }"><strong>{{ row.name }}</strong><small>{{ row.facility }}</small></template>
        </el-table-column>
        <el-table-column label="状态" width="130">
          <template #default="{ row }">
            <GateStateBadge v-if="config.key === 'gateUnit'" :state="row.status" />
            <StatusBadge v-else :status="row.status" />
          </template>
        </el-table-column>
		<el-table-column v-if="config.key === 'operationDirective'" label="目标状态" width="120">
          <template #default="{ row }"><GateStateBadge :state="row.gateState || 'closed'" /></template>
        </el-table-column>
		<el-table-column label="风险" width="80"><template #default="{ row }">{{ riskLabel(row.riskLevel) }}</template></el-table-column>
        <el-table-column prop="owner" label="责任人" min-width="110" />
		<el-table-column v-if="['gateUnit', 'operationDirective', 'executionConfirmation'].includes(config.key)" prop="relatedCode" :label="relationLabel" width="130" />
        <el-table-column v-if="config.key === 'reservoir'" label="当前水位" width="110">
          <template #default="{ row }">{{ formatLevel(row.waterLevel) }}</template>
        </el-table-column>
        <el-table-column v-if="config.key === 'reservoir'" label="许可区间" min-width="170">
          <template #default="{ row }">
            <span>{{ rangeText(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column v-if="config.key === 'operationDirective'" label="所属库区水位" min-width="180">
          <template #default="{ row }">
            <div class="permit-cell">
              <strong v-if="row.reservoirCode">{{ row.reservoirCode }} · {{ formatLevel(row.reservoirWaterLevel) }}</strong>
              <span v-else class="muted">未关联库区</span>
              <small>{{ row.hasWaterLevelRange ? `区间 ${rangeText(row)}` : '库区未设区间' }}</small>
            </div>
          </template>
        </el-table-column>
        <el-table-column v-if="config.key === 'operationDirective'" label="本次放行" min-width="220">
          <template #default="{ row }">
            <el-tooltip v-if="directivePermit(row).show" :content="directivePermit(row).text" placement="top">
              <span :class="`permit permit--${directivePermit(row).permitted ? 'pass' : 'block'}`">
                {{ directivePermit(row).permitted ? '可放行' : '拦截·留已复核' }}
              </span>
            </el-tooltip>
            <span v-else class="muted">未到执行环节</span>
            <small v-if="directivePermit(row).show" class="permit-detail">{{ directivePermit(row).text }}</small>
          </template>
        </el-table-column>
        <el-table-column label="指标" width="105"><template #default="{ row }">{{ row.metricValue }} {{ row.metricUnit }}</template></el-table-column>
        <el-table-column label="更新时间" width="165"><template #default="{ row }">{{ formatDate(row.updatedAt) }}</template></el-table-column>
        <el-table-column label="操作" min-width="220" fixed="right">
          <template #default="{ row }">
            <div v-if="transitionsFor(row).length" class="row-actions">
			  <el-button v-for="target in transitionsFor(row)" :key="target" link type="primary" @click="selectTransition(row, target)">推进至{{ statusLabel(target) }}</el-button>
            </div>
            <span v-else class="muted">当前角色无可执行动作</span>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <ConfirmDialog v-model="showCreate" :title="`新增${config.label}`" confirm-label="创建记录" :confirm-disabled="!createReady" :loading="store.loading" @confirm="createRecord">
      <el-form class="record-form" label-position="top">
		<el-alert v-if="['gateUnit', 'operationDirective', 'executionConfirmation'].includes(config.key) && !relatedOptions.length" :title="`当前没有可用的${relationLabel}`" type="warning" show-icon />
        <div class="form-grid">
          <el-form-item label="业务编码"><el-input v-model="createForm.code" /></el-form-item>
          <el-form-item label="名称"><el-input v-model="createForm.name" /></el-form-item>
		  <el-form-item v-if="['gateUnit', 'operationDirective', 'executionConfirmation'].includes(config.key)" :label="relationLabel">
			<el-select :model-value="createForm.relatedCode" filterable @update:model-value="selectRelated">
				<el-option v-for="item in relatedOptions" :key="item.id" :label="`${item.code} · ${item.name}`" :value="item.code" />
			</el-select>
		  </el-form-item>
		  <el-form-item label="作业区域"><el-input v-model="createForm.facility" :disabled="['gateUnit', 'operationDirective', 'executionConfirmation'].includes(config.key)" /></el-form-item>
          <el-form-item label="责任人"><el-input v-model="createForm.owner" /></el-form-item>
		  <el-form-item label="业务类别"><el-input v-model="createForm.category" /></el-form-item>
		  <el-form-item label="风险等级"><el-select v-model="createForm.riskLevel"><el-option v-for="risk in ['low', 'medium', 'high', 'critical']" :key="risk" :label="riskLabel(risk)" :value="risk" /></el-select></el-form-item>
		  <el-form-item :label="metricLabel"><el-input-number v-model="createForm.metricValue" :min="0" :precision="2" controls-position="right" /></el-form-item>
		  <el-form-item label="指标单位"><el-input v-model="createForm.metricUnit" /></el-form-item>
		  <el-form-item v-if="config.key === 'operationDirective'" label="目标闸门状态"><el-select v-model="createForm.gateState"><el-option v-for="state in ['open', 'closed', 'locked']" :key="state" :label="statusLabel(state)" :value="state" /></el-select></el-form-item>
		  <el-form-item v-if="config.key === 'reservoir'" label="当前水位（米）"><el-input-number v-model="createForm.waterLevel" :precision="2" controls-position="right" /></el-form-item>
		  <el-form-item v-if="config.key === 'reservoir'" label="水位许可下限（米，留空表示不设区间）"><el-input-number v-model="createForm.waterLevelLower" :precision="2" controls-position="right" :placeholder="null" /></el-form-item>
		  <el-form-item v-if="config.key === 'reservoir'" label="水位许可上限（米，留空表示不设区间）"><el-input-number v-model="createForm.waterLevelUpper" :precision="2" controls-position="right" /></el-form-item>
        </div>
		<el-form-item label="业务说明"><el-input v-model="createForm.description" type="textarea" :rows="2" maxlength="1000" show-word-limit /></el-form-item>
        <el-form-item label="现场证据"><el-input v-model="createForm.evidence" type="textarea" :rows="3" /></el-form-item>
      </el-form>
    </ConfirmDialog>

    <ConfirmDialog :model-value="Boolean(pending)" title="确认状态迁移" confirm-label="确认并记录审计" @update:model-value="pending = null" @confirm="confirmTransition">
      <p>此次操作会校验角色和版本，并将状态、请求 ID 与审计证据原子写入。</p>
      <div class="transition-summary"><StatusBadge :status="pending?.item.status || ''" /><span>到</span><StatusBadge :status="pending?.status || ''" /></div>
      <el-input v-model="transitionReason" type="textarea" :rows="3" maxlength="500" show-word-limit aria-label="迁移原因" />
    </ConfirmDialog>
  </main>
</template>
