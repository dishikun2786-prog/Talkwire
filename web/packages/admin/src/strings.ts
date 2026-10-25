/** Built-in texts of the administrator pages; pass your own to `<ImAdminProvider strings>` for other languages. */
export interface AdminStrings {
  chatRecords: string;
  callRecords: string;
  callDetail: string;
  userId: string;
  userIdHint: string;
  search: string;
  loading: string;
  loadMore: string;
  loadOlder: string;
  noTopics: string;
  noMessages: string;
  noCalls: string;
  pickTopic: string;
  group: string;
  direct: string;
  messages: (n: number) => string;
  someone: string;
  allTypes: string;
  types: Record<"text" | "image" | "video" | "audio" | "file", string>;
  keyword: string;
  from: string;
  to: string;
  apply: string;
  open: string;
  // clearing
  clear: string;
  clearTitle: string;
  clearWarning: string;
  reason: string;
  reasonHint: string;
  typeToConfirm: (text: string) => string;
  cancel: string;
  clearing: string;
  cleared: (messages: number, media: number) => string;
  clearedBefore: string;
  close: string;
  networkError: string;
  unreachable: string;
  system: string;
  // calls
  caller: string;
  callee: string;
  time: string;
  type: string;
  status: string;
  outcome: string;
  duration: string;
  allStatuses: string;
  allOutcomes: string;
  callTypes: Record<"voice" | "video", string>;
  statuses: Record<"ringing" | "active", string>;
  outcomes: Record<string, string>;
  /** An end reason the table above does not name. */
  endedOther: (reason: string) => string;
  requested: string;
  timeline: string;
  steps: Record<"created" | "answered" | "upgraded" | "ended", string>;
  callId: string;
  back: string;
  notFound: string;
}

export const adminEn: AdminStrings = {
  chatRecords: "Chat records",
  callRecords: "Call records",
  callDetail: "Call",
  userId: "User ID",
  userIdHint: "The user's ID in your system",
  search: "Search",
  loading: "Loading…",
  loadMore: "Load more",
  loadOlder: "Load older",
  noTopics: "No conversations",
  noMessages: "No messages",
  noCalls: "No calls",
  pickTopic: "Pick a conversation",
  group: "Group",
  direct: "Direct",
  messages: (n) => `${n} messages`,
  someone: "Unknown user",
  allTypes: "All types",
  types: { text: "Text", image: "Image", video: "Video", audio: "Voice", file: "File" },
  keyword: "Keyword",
  from: "From",
  to: "To",
  apply: "Apply",
  open: "Details",
  clear: "Clear chat records",
  clearTitle: "Clear this conversation for both users?",
  clearWarning: "Every message and file in it is deleted for good, on both sides. This cannot be undone.",
  reason: "Reason",
  reasonHint: "Kept in the audit log",
  typeToConfirm: (text) => `Type ${text} to confirm`,
  cancel: "Cancel",
  clearing: "Clearing…",
  cleared: (m, f) => `Deleted ${m} messages and ${f} files.`,
  clearedBefore: "This was already done earlier.",
  close: "Close",
  networkError: "The server did not answer. Try again: it will not clear twice.",
  unreachable: "The server did not answer",
  system: "System",
  caller: "Caller",
  callee: "Callee",
  time: "Time",
  type: "Type",
  status: "Status",
  outcome: "Outcome",
  duration: "Duration",
  allStatuses: "All statuses",
  allOutcomes: "All outcomes",
  callTypes: { voice: "Voice", video: "Video" },
  statuses: { ringing: "Ringing", active: "In a call" },
  outcomes: {
    user_hangup: "Ended",
    no_answer: "Missed",
    rejected: "Declined",
    cancelled: "Cancelled",
    disconnected: "Connection lost",
    failed: "Failed",
  },
  endedOther: (r) => `Ended (${r})`,
  requested: "asked for",
  timeline: "Timeline",
  steps: { created: "Started", answered: "Answered", upgraded: "Switched to video", ended: "Ended" },
  callId: "Call ID",
  back: "Back",
  notFound: "No such call",
};

export const adminZh: AdminStrings = {
  chatRecords: "聊天记录",
  callRecords: "通话记录",
  callDetail: "通话详情",
  userId: "用户 ID",
  userIdHint: "用户在你系统里的 ID",
  search: "查询",
  loading: "加载中…",
  loadMore: "加载更多",
  loadOlder: "加载更早",
  noTopics: "没有会话",
  noMessages: "没有消息",
  noCalls: "没有通话",
  pickTopic: "选择一个会话",
  group: "群聊",
  direct: "单聊",
  messages: (n) => `${n} 条消息`,
  someone: "未知用户",
  allTypes: "全部类型",
  types: { text: "文字", image: "图片", video: "视频", audio: "语音", file: "文件" },
  keyword: "关键词",
  from: "开始日期",
  to: "结束日期",
  apply: "筛选",
  open: "详情",
  clear: "清空聊天记录",
  clearTitle: "为双方清空这个会话？",
  clearWarning: "会话里的全部消息和文件会为双方永久删除，无法恢复。",
  reason: "原因",
  reasonHint: "会写进审计记录",
  typeToConfirm: (text) => `输入 ${text} 确认`,
  cancel: "取消",
  clearing: "正在清空…",
  cleared: (m, f) => `已删除 ${m} 条消息、${f} 个文件。`,
  clearedBefore: "这次清空之前已经完成。",
  close: "关闭",
  networkError: "服务器没有响应。可以重试，不会重复清空。",
  unreachable: "服务器没有响应",
  system: "系统",
  caller: "主叫",
  callee: "被叫",
  time: "发起时间",
  type: "类型",
  status: "状态",
  outcome: "结果",
  duration: "时长",
  allStatuses: "全部状态",
  allOutcomes: "全部结果",
  callTypes: { voice: "语音", video: "视频" },
  statuses: { ringing: "振铃中", active: "通话中" },
  outcomes: {
    user_hangup: "已结束",
    no_answer: "未接听",
    rejected: "已拒绝",
    cancelled: "已取消",
    disconnected: "连接中断",
    failed: "失败",
  },
  endedOther: (r) => `已结束（${r}）`,
  requested: "发起时为",
  timeline: "时间线",
  steps: { created: "发起", answered: "接听", upgraded: "切换到视频", ended: "结束" },
  callId: "通话 ID",
  back: "返回",
  notFound: "找不到这通通话",
};

export const adminVi: AdminStrings = {
  chatRecords: "Lịch sử trò chuyện",
  callRecords: "Lịch sử cuộc gọi",
  callDetail: "Cuộc gọi",
  userId: "ID người dùng",
  userIdHint: "ID của người dùng trong hệ thống của bạn",
  search: "Tìm",
  loading: "Đang tải…",
  loadMore: "Tải thêm",
  loadOlder: "Tải cũ hơn",
  noTopics: "Không có cuộc trò chuyện",
  noMessages: "Không có tin nhắn",
  noCalls: "Không có cuộc gọi",
  pickTopic: "Chọn một cuộc trò chuyện",
  group: "Nhóm",
  direct: "Riêng",
  messages: (n) => `${n} tin nhắn`,
  someone: "Người dùng không rõ",
  allTypes: "Mọi loại",
  types: { text: "Văn bản", image: "Ảnh", video: "Video", audio: "Thoại", file: "Tệp" },
  keyword: "Từ khóa",
  from: "Từ ngày",
  to: "Đến ngày",
  apply: "Lọc",
  open: "Chi tiết",
  clear: "Xóa lịch sử trò chuyện",
  clearTitle: "Xóa cuộc trò chuyện này cho cả hai người?",
  clearWarning: "Mọi tin nhắn và tệp trong đó bị xóa vĩnh viễn ở cả hai phía. Không thể hoàn tác.",
  reason: "Lý do",
  reasonHint: "Được lưu vào nhật ký kiểm tra",
  typeToConfirm: (text) => `Nhập ${text} để xác nhận`,
  cancel: "Hủy",
  clearing: "Đang xóa…",
  cleared: (m, f) => `Đã xóa ${m} tin nhắn và ${f} tệp.`,
  clearedBefore: "Việc này đã được thực hiện trước đó.",
  close: "Đóng",
  networkError: "Máy chủ không phản hồi. Hãy thử lại: sẽ không xóa hai lần.",
  unreachable: "Máy chủ không phản hồi",
  system: "Hệ thống",
  caller: "Người gọi",
  callee: "Người nhận",
  time: "Thời gian",
  type: "Loại",
  status: "Trạng thái",
  outcome: "Kết quả",
  duration: "Thời lượng",
  allStatuses: "Mọi trạng thái",
  allOutcomes: "Mọi kết quả",
  callTypes: { voice: "Thoại", video: "Video" },
  statuses: { ringing: "Đang đổ chuông", active: "Đang gọi" },
  outcomes: {
    user_hangup: "Đã kết thúc",
    no_answer: "Nhỡ",
    rejected: "Đã từ chối",
    cancelled: "Đã hủy",
    disconnected: "Mất kết nối",
    failed: "Thất bại",
  },
  endedOther: (r) => `Đã kết thúc (${r})`,
  requested: "yêu cầu",
  timeline: "Diễn biến",
  steps: { created: "Bắt đầu", answered: "Trả lời", upgraded: "Chuyển sang video", ended: "Kết thúc" },
  callId: "ID cuộc gọi",
  back: "Quay lại",
  notFound: "Không có cuộc gọi này",
};

/** The built-in table for a BCP 47 tag; English when the language is not built in. */
export function adminStringsFor(locale: string): AdminStrings {
  const lang = locale.toLowerCase().split("-")[0];
  return lang === "zh" ? adminZh : lang === "vi" ? adminVi : adminEn;
}
