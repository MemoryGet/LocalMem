export interface SuiteStartEvent {
  type: 'suite_start'
  suite: string
  total?: number
  ts?: string
}

export interface CaseStartEvent {
  type: 'case_start'
  suite: string
  name: string
  ts?: string
}

export interface StepEvent {
  type: 'step' | 'input' | 'output'
  suite: string
  name: string
  seq?: number
  action?: string
  detail?: string
  status?: string
  label?: string
  value?: string
}

export interface CaseEndEvent {
  type: 'case_end'
  suite: string
  name: string
  status: 'pass' | 'fail' | 'skip'
  duration_ms: number
}

export interface SuiteEndEvent {
  type: 'suite_end'
  suite: string
  passed: number
  failed: number
  duration_ms: number
}

export interface DoneEvent {
  type: 'done'
  total_passed: number
  total_failed: number
}

export interface LogEvent {
  type: 'log'
  suite: string
  name: string
  text: string
}

export interface ErrorEvent {
  type: 'error'
  msg: string
}

export interface StoppedEvent {
  type: 'stopped'
  completed: number
  total: number
}

export interface DescriptionEvent {
  type: 'description'
  suite: string
  name: string
  desc: string
}

export interface SnapshotEvent {
  type: 'snapshot'
  running: boolean
  replayed: number
}

export type TestEvent =
  | SuiteStartEvent | CaseStartEvent | StepEvent
  | CaseEndEvent | SuiteEndEvent | DoneEvent
  | LogEvent | ErrorEvent | StoppedEvent | SnapshotEvent
  | DescriptionEvent
