import { toScopePolicy } from './scope'
import type {
  Contact,
  DirectorySyncResult,
  Group,
  GroupMember,
  PageResult,
} from '../contracts'
import {
  asRecord,
  contractError,
  readNumber,
  readOptionalTime,
  readPageEnvelope,
  readString,
} from './guard'

const CONTACTS_ENDPOINT = '/api/v1/contacts'
const GROUPS_ENDPOINT = '/api/v1/groups'
const MEMBERS_ENDPOINT = '/api/v1/groups/{id}/members'
const SYNC_ENDPOINT = '/api/v1/directory/sync'

function toContact(raw: unknown): Contact {
  const record = asRecord(raw, CONTACTS_ENDPOINT, 'contact')
  const contact: Contact = {
    userId: readNumber(record, 'UserID', CONTACTS_ENDPOINT),
    ...(record.Policy == null ? {} : { policy: toScopePolicy(record.Policy) }),
    nickname: readString(record, 'Nickname', CONTACTS_ENDPOINT, ''),
    remark: readString(record, 'Remark', CONTACTS_ENDPOINT, ''),
    lastSeenAt: readOptionalTime(record, 'LastSeenAt', CONTACTS_ENDPOINT),
  }
  return contact
}

function toGroup(raw: unknown): Group {
  const record = asRecord(raw, GROUPS_ENDPOINT, 'group')
  return {
    groupId: readNumber(record, 'GroupID', GROUPS_ENDPOINT),
    ...(record.Policy == null ? {} : { policy: toScopePolicy(record.Policy) }),
    name: readString(record, 'Name', GROUPS_ENDPOINT, ''),
    memberCount: readNumber(record, 'MemberCount', GROUPS_ENDPOINT, 0),
    lastSeenAt: readOptionalTime(record, 'LastSeenAt', GROUPS_ENDPOINT),
  }
}

function toMember(raw: unknown): GroupMember {
  const record = asRecord(raw, MEMBERS_ENDPOINT, 'member')
  return {
    groupId: readNumber(record, 'GroupID', MEMBERS_ENDPOINT),
    userId: readNumber(record, 'UserID', MEMBERS_ENDPOINT),
    nickname: readString(record, 'Nickname', MEMBERS_ENDPOINT, ''),
    card: readString(record, 'Card', MEMBERS_ENDPOINT, ''),
    role: readString(record, 'Role', MEMBERS_ENDPOINT, ''),
    lastSeenAt: readOptionalTime(record, 'LastSeenAt', MEMBERS_ENDPOINT),
  }
}

/**
 * The catalogue endpoints serialise domain structs without JSON tags, so the
 * page envelope is PascalCase. Page and limit come back normalised by the
 * backend, which is what the pagination control displays.
 */
export function toContactPage(raw: unknown): PageResult<Contact> {
  const page = readPageEnvelope(raw, CONTACTS_ENDPOINT, 'Items')
  return {
    items: page.items.map(toContact),
    total: page.total,
    page: page.page,
    limit: page.limit,
  }
}

export function toGroupPage(raw: unknown): PageResult<Group> {
  const page = readPageEnvelope(raw, GROUPS_ENDPOINT, 'Items')
  return {
    items: page.items.map(toGroup),
    total: page.total,
    page: page.page,
    limit: page.limit,
  }
}

export function toMemberPage(raw: unknown): PageResult<GroupMember> {
  const page = readPageEnvelope(raw, MEMBERS_ENDPOINT, 'Items')
  return {
    items: page.items.map(toMember),
    total: page.total,
    page: page.page,
    limit: page.limit,
  }
}

/** Normalises the `{BotID,Contacts,Groups,Members}` sync result. */
export function toDirectorySyncResult(raw: unknown): DirectorySyncResult {
  const record = asRecord(raw, SYNC_ENDPOINT, 'sync')
  const botId = record.BotID
  if (typeof botId !== 'number' || !Number.isFinite(botId)) {
    throw contractError(SYNC_ENDPOINT, 'BotID 不是有效数字')
  }
  return {
    botId,
    contacts: readNumber(record, 'Contacts', SYNC_ENDPOINT, 0),
    groups: readNumber(record, 'Groups', SYNC_ENDPOINT, 0),
    members: readNumber(record, 'Members', SYNC_ENDPOINT, 0),
  }
}
