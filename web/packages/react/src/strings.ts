/**
 * Every text the components show (5.9). The keys are the Android kit's
 * resource names without the `imkit_` prefix, camel-cased; an app replaces
 * any of them through `<ImKitProvider strings>`. Placeholders are `{0}`,
 * `{1}` in the order of the Android `%1$s`, `%2$s`.
 */

export interface ImStrings {
  previewImage: string;
  previewVideo: string;
  previewVoice: string;
  previewFile: string;
  previewUnsupported: string;
  unsupportedMessage: string;
  voiceSeconds: string;
  typeMessage: string;
  send: string;
  holdToTalk: string;
  releaseToSend: string;
  releaseToCancel: string;
  swipeUpCancel: string;
  recordTooShort: string;
  micPermission: string;
  keyboard: string;
  voiceInput: string;
  attach: string;
  attachFile: string;
  newMessages: string;
  today: string;
  yesterday: string;
  jumpToBottom: string;
  forwarded: string;
  sending: string;
  sent: string;
  read: string;
  sendFailedRetry: string;
  sendFailed: string;
  readByCount: string;
  typing: string;
  typingNamed: string;
  /** The group assistant's name when the host does not name it. */
  assistant: string;
  online: string;
  lastSeenMinutes: string;
  lastSeenHours: string;
  lastSeenDays: string;
  connecting: string;
  offlineBanner: string;
  loadOlderFailed: string;
  actionCopy: string;
  actionRecall: string;
  actionRetry: string;
  actionDiscard: string;
  recallFailed: string;
  actionFailed: string;
  tooLarge: string;
  sendMediaFailed: string;
  chats: string;
  noConversations: string;
  senderPreview: string;
  draft: string;
  justNow: string;
  minutesAgo: string;
  hoursAgo: string;
  you: string;
  removedFromGroup: string;
  historyCleared: string;
  loading: string;
  back: string;
  groupInfo: string;
  chatInfo: string;
  membersCount: string;
  roleOwner: string;
  roleAdmin: string;
  addMembers: string;
  membersPrivate: string;
  renameGroup: string;
  pinChat: string;
  muteNotifications: string;
  clearHistory: string;
  clearHistoryConfirm: string;
  disbandGroup: string;
  disbandConfirm: string;
  leaveGroup: string;
  leaveConfirm: string;
  makeAdmin: string;
  removeAdmin: string;
  adminLimit: string;
  removeMember: string;
  removeConfirm: string;
  transferOwner: string;
  transferConfirm: string;
  save: string;
  cancel: string;
  contacts: string;
  noContacts: string;
  createGroup: string;
  groupName: string;
  addCount: string;
  createCount: string;
  search: string;
  actionReply: string;
  actionForward: string;
  actionSelect: string;
  actionSave: string;
  replyingTo: string;
  selectedCount: string;
  forwardTo: string;
  sendCount: string;
  noChats: string;
  forwardedOk: string;
  forwardFailed: string;
  copied: string;
  close: string;
  pin: string;
  unpin: string;
  mute: string;
  unmute: string;
  deleteChat: string;
  deleteChatConfirm: string;
  newChat: string;
  ok: string;
  readByFooterPartial: string;
  readByFooterAll: string;
  emoji: string;
  recalledPreview: string;
  recalledByYou: string;
  recalledBy: string;
  searchMessages: string;
  noResults: string;
  searchChats: string;
}

export const en: ImStrings = {
  previewImage: "[Image]",
  previewVideo: "[Video]",
  previewVoice: "[Voice]",
  previewFile: "[File] {0}",
  previewUnsupported: "[Unsupported message]",
  unsupportedMessage: "This message can't be shown in this version",
  voiceSeconds: "{0}″",
  typeMessage: "Message",
  send: "Send",
  holdToTalk: "Hold to talk",
  releaseToSend: "Release to send",
  releaseToCancel: "Release to cancel",
  swipeUpCancel: "Slide up to cancel",
  recordTooShort: "Too short to send",
  micPermission: "Allow microphone access to send voice messages",
  keyboard: "Keyboard",
  voiceInput: "Voice message",
  attach: "Attach",
  attachFile: "File",
  newMessages: "New messages",
  today: "Today",
  yesterday: "Yesterday",
  jumpToBottom: "Latest messages",
  forwarded: "Forwarded",
  sending: "Sending",
  sent: "Sent",
  read: "Read",
  sendFailedRetry: "Not sent. Tap to retry",
  sendFailed: "Not sent",
  readByCount: "Read by {0}",
  typing: "Typing…",
  typingNamed: "{0} is typing…",
  assistant: "Assistant",
  online: "Online",
  lastSeenMinutes: "Last seen {0} min ago",
  lastSeenHours: "Last seen {0} h ago",
  lastSeenDays: "Last seen {0} d ago",
  connecting: "Connecting…",
  offlineBanner: "No connection. Messages will be sent when you're back online.",
  loadOlderFailed: "Couldn't load earlier messages",
  actionCopy: "Copy",
  actionRecall: "Unsend",
  actionRetry: "Send again",
  actionDiscard: "Delete",
  recallFailed: "Couldn't unsend this message",
  actionFailed: "Something went wrong. Please try again.",
  tooLarge: "This file is too large",
  sendMediaFailed: "Couldn't send it. Please try again.",
  chats: "Chats",
  noConversations: "No chats yet. Start one from your contacts.",
  senderPreview: "{0}: {1}",
  draft: "[Draft]",
  justNow: "Just now",
  minutesAgo: "{0} min ago",
  hoursAgo: "{0} h ago",
  you: "You",
  removedFromGroup: "You're no longer in this group",
  historyCleared: "Chat history was cleared",
  loading: "Loading…",
  back: "Back",
  groupInfo: "Group info",
  chatInfo: "Chat info",
  membersCount: "Members ({0})",
  roleOwner: "Owner",
  roleAdmin: "Admin",
  addMembers: "Add members",
  membersPrivate: "Members can't view each other's profiles or add each other.",
  renameGroup: "Rename group",
  pinChat: "Pin chat",
  muteNotifications: "Mute notifications",
  clearHistory: "Clear chat history",
  clearHistoryConfirm: "Clear the history for everyone in this chat? This can't be undone.",
  disbandGroup: "Disband group",
  disbandConfirm: "Disband this group? Everyone will be removed.",
  leaveGroup: "Leave group",
  leaveConfirm: "Leave this group?",
  makeAdmin: "Make admin",
  removeAdmin: "Remove as admin",
  adminLimit: "This group already has as many admins as it can have",
  removeMember: "Remove from group",
  removeConfirm: "Remove {0} from the group?",
  transferOwner: "Make owner",
  transferConfirm: "Make {0} the owner? You'll become a member.",
  save: "Save",
  cancel: "Cancel",
  contacts: "Contacts",
  noContacts: "No contacts",
  createGroup: "New group",
  groupName: "Group name",
  addCount: "Add ({0})",
  createCount: "Create ({0})",
  search: "Search",
  actionReply: "Reply",
  actionForward: "Forward",
  actionSelect: "Select",
  actionSave: "Save",
  replyingTo: "Reply to {0}",
  selectedCount: "{0} selected",
  forwardTo: "Forward to",
  sendCount: "Send ({0})",
  noChats: "No chats yet",
  forwardedOk: "Forwarded",
  forwardFailed: "Couldn't forward to {0} chats",
  copied: "Copied",
  close: "Close",
  pin: "Pin",
  unpin: "Unpin",
  mute: "Mute",
  unmute: "Unmute",
  deleteChat: "Delete chat",
  deleteChatConfirm: "Delete this chat? Its messages are removed from this device only.",
  newChat: "New chat",
  ok: "OK",
  readByFooterPartial: "{0} members · {1} haven't read it",
  readByFooterAll: "All {0} members have read it",
  emoji: "Emoji",
  recalledPreview: "Message unsent",
  recalledByYou: "You unsent a message",
  recalledBy: "{0} unsent a message",
  searchMessages: "Messages",
  noResults: "No results",
  searchChats: "Search chats and messages",
};

export const zh: ImStrings = {
  previewImage: "[图片]",
  previewVideo: "[视频]",
  previewVoice: "[语音]",
  previewFile: "[文件] {0}",
  previewUnsupported: "[暂不支持的消息]",
  unsupportedMessage: "当前版本无法显示这条消息",
  voiceSeconds: "{0}″",
  typeMessage: "发消息",
  send: "发送",
  holdToTalk: "按住 说话",
  releaseToSend: "松开 发送",
  releaseToCancel: "松开 取消",
  swipeUpCancel: "上滑取消",
  recordTooShort: "说话时间太短",
  micPermission: "允许使用麦克风后才能发语音",
  keyboard: "键盘",
  voiceInput: "语音",
  attach: "更多",
  attachFile: "文件",
  newMessages: "以下为新消息",
  today: "今天",
  yesterday: "昨天",
  jumpToBottom: "回到最新",
  forwarded: "转发",
  sending: "发送中",
  sent: "已发送",
  read: "已读",
  sendFailedRetry: "发送失败，点击重发",
  sendFailed: "发送失败",
  readByCount: "{0} 人已读",
  typing: "对方正在输入…",
  typingNamed: "{0} 正在输入…",
  assistant: "助手",
  online: "在线",
  lastSeenMinutes: "{0} 分钟前在线",
  lastSeenHours: "{0} 小时前在线",
  lastSeenDays: "{0} 天前在线",
  connecting: "连接中…",
  offlineBanner: "网络已断开，恢复后会自动发送",
  loadOlderFailed: "更早的消息加载失败",
  actionCopy: "复制",
  actionRecall: "撤回",
  actionRetry: "重新发送",
  actionDiscard: "删除",
  recallFailed: "撤回失败",
  actionFailed: "操作失败，请重试",
  tooLarge: "文件太大",
  sendMediaFailed: "发送失败，请重试",
  chats: "聊天",
  noConversations: "还没有聊天，从通讯录发起一个吧",
  senderPreview: "{0}：{1}",
  draft: "[草稿]",
  justNow: "刚刚",
  minutesAgo: "{0} 分钟前",
  hoursAgo: "{0} 小时前",
  you: "我",
  removedFromGroup: "你已不在该群",
  historyCleared: "聊天记录已清空",
  loading: "加载中…",
  back: "返回",
  groupInfo: "群聊信息",
  chatInfo: "聊天信息",
  membersCount: "群成员（{0}）",
  roleOwner: "群主",
  roleAdmin: "管理员",
  addMembers: "添加成员",
  membersPrivate: "群成员之间不可查看资料或添加好友",
  renameGroup: "修改群名",
  pinChat: "置顶聊天",
  muteNotifications: "消息免打扰",
  clearHistory: "清空聊天记录",
  clearHistoryConfirm: "为聊天中的所有人清空记录？此操作无法撤销。",
  disbandGroup: "解散群聊",
  disbandConfirm: "确定解散群聊？所有成员都将被移出。",
  leaveGroup: "退出群聊",
  leaveConfirm: "确定退出群聊？",
  makeAdmin: "设为管理员",
  removeAdmin: "取消管理员",
  adminLimit: "管理员人数已达上限",
  removeMember: "移出群聊",
  removeConfirm: "将 {0} 移出群聊？",
  transferOwner: "转让群主",
  transferConfirm: "将群主转让给 {0}？你将成为普通成员。",
  save: "保存",
  cancel: "取消",
  contacts: "通讯录",
  noContacts: "暂无联系人",
  createGroup: "创建群聊",
  groupName: "群聊名称",
  addCount: "添加（{0}）",
  createCount: "创建（{0}）",
  search: "搜索",
  actionReply: "回复",
  actionForward: "转发",
  actionSelect: "多选",
  actionSave: "保存",
  replyingTo: "回复 {0}",
  selectedCount: "已选 {0} 条",
  forwardTo: "转发给",
  sendCount: "发送（{0}）",
  noChats: "还没有会话",
  forwardedOk: "已转发",
  forwardFailed: "{0} 个会话转发失败",
  copied: "已复制",
  close: "关闭",
  pin: "置顶",
  unpin: "取消置顶",
  mute: "免打扰",
  unmute: "取消免打扰",
  deleteChat: "删除聊天",
  deleteChatConfirm: "删除该聊天？只会清除本机上的记录。",
  newChat: "发起聊天",
  ok: "确定",
  readByFooterPartial: "共 {0} 人 · 还有 {1} 人未读",
  readByFooterAll: "共 {0} 人，全部已读",
  emoji: "表情",
  recalledPreview: "撤回了一条消息",
  recalledByYou: "你撤回了一条消息",
  recalledBy: "{0} 撤回了一条消息",
  searchMessages: "聊天记录",
  noResults: "无结果",
  searchChats: "搜索聊天和聊天记录",
};

export const vi: ImStrings = {
  previewImage: "[Hình ảnh]",
  previewVideo: "[Video]",
  previewVoice: "[Tin nhắn thoại]",
  previewFile: "[Tệp] {0}",
  previewUnsupported: "[Tin nhắn chưa hỗ trợ]",
  unsupportedMessage: "Phiên bản này chưa hiển thị được tin nhắn này",
  voiceSeconds: "{0}″",
  typeMessage: "Nhắn tin",
  send: "Gửi",
  holdToTalk: "Giữ để nói",
  releaseToSend: "Thả để gửi",
  releaseToCancel: "Thả để hủy",
  swipeUpCancel: "Vuốt lên để hủy",
  recordTooShort: "Quá ngắn để gửi",
  micPermission: "Cho phép dùng micro để gửi tin nhắn thoại",
  keyboard: "Bàn phím",
  voiceInput: "Tin nhắn thoại",
  attach: "Đính kèm",
  attachFile: "Tệp",
  newMessages: "Tin nhắn mới",
  today: "Hôm nay",
  yesterday: "Hôm qua",
  jumpToBottom: "Tin mới nhất",
  forwarded: "Đã chuyển tiếp",
  sending: "Đang gửi",
  sent: "Đã gửi",
  read: "Đã xem",
  sendFailedRetry: "Chưa gửi được. Nhấn để gửi lại",
  sendFailed: "Chưa gửi được",
  readByCount: "{0} người đã xem",
  typing: "Đang soạn tin…",
  typingNamed: "{0} đang soạn tin…",
  assistant: "Trợ lý",
  online: "Đang hoạt động",
  lastSeenMinutes: "Hoạt động {0} phút trước",
  lastSeenHours: "Hoạt động {0} giờ trước",
  lastSeenDays: "Hoạt động {0} ngày trước",
  connecting: "Đang kết nối…",
  offlineBanner: "Mất kết nối. Tin nhắn sẽ được gửi khi có mạng trở lại.",
  loadOlderFailed: "Không tải được tin nhắn cũ hơn",
  actionCopy: "Sao chép",
  actionRecall: "Thu hồi",
  actionRetry: "Gửi lại",
  actionDiscard: "Xóa",
  recallFailed: "Không thu hồi được tin nhắn",
  actionFailed: "Đã xảy ra lỗi. Vui lòng thử lại.",
  tooLarge: "Tệp quá lớn",
  sendMediaFailed: "Không gửi được. Vui lòng thử lại.",
  chats: "Trò chuyện",
  noConversations: "Chưa có cuộc trò chuyện. Hãy bắt đầu từ danh bạ.",
  senderPreview: "{0}: {1}",
  draft: "[Bản nháp]",
  justNow: "Vừa xong",
  minutesAgo: "{0} phút trước",
  hoursAgo: "{0} giờ trước",
  you: "Bạn",
  removedFromGroup: "Bạn không còn trong nhóm này",
  historyCleared: "Lịch sử trò chuyện đã bị xóa",
  loading: "Đang tải…",
  back: "Quay lại",
  groupInfo: "Thông tin nhóm",
  chatInfo: "Thông tin trò chuyện",
  membersCount: "Thành viên ({0})",
  roleOwner: "Trưởng nhóm",
  roleAdmin: "Quản trị viên",
  addMembers: "Thêm thành viên",
  membersPrivate: "Thành viên không thể xem hồ sơ hoặc kết bạn với nhau.",
  renameGroup: "Đổi tên nhóm",
  pinChat: "Ghim cuộc trò chuyện",
  muteNotifications: "Tắt thông báo",
  clearHistory: "Xóa lịch sử trò chuyện",
  clearHistoryConfirm: "Xóa lịch sử cho mọi người trong cuộc trò chuyện? Không thể hoàn tác.",
  disbandGroup: "Giải tán nhóm",
  disbandConfirm: "Giải tán nhóm? Mọi người sẽ bị xóa khỏi nhóm.",
  leaveGroup: "Rời nhóm",
  leaveConfirm: "Rời khỏi nhóm này?",
  makeAdmin: "Đặt làm quản trị viên",
  removeAdmin: "Bỏ quyền quản trị viên",
  adminLimit: "Nhóm đã có số quản trị viên tối đa",
  removeMember: "Xóa khỏi nhóm",
  removeConfirm: "Xóa {0} khỏi nhóm?",
  transferOwner: "Chuyển quyền trưởng nhóm",
  transferConfirm: "Chuyển quyền trưởng nhóm cho {0}? Bạn sẽ trở thành thành viên.",
  save: "Lưu",
  cancel: "Hủy",
  contacts: "Danh bạ",
  noContacts: "Chưa có liên hệ",
  createGroup: "Tạo nhóm",
  groupName: "Tên nhóm",
  addCount: "Thêm ({0})",
  createCount: "Tạo ({0})",
  search: "Tìm kiếm",
  actionReply: "Trả lời",
  actionForward: "Chuyển tiếp",
  actionSelect: "Chọn nhiều",
  actionSave: "Lưu",
  replyingTo: "Trả lời {0}",
  selectedCount: "Đã chọn {0}",
  forwardTo: "Chuyển tiếp đến",
  sendCount: "Gửi ({0})",
  noChats: "Chưa có cuộc trò chuyện",
  forwardedOk: "Đã chuyển tiếp",
  forwardFailed: "Không chuyển tiếp được đến {0} cuộc trò chuyện",
  copied: "Đã sao chép",
  close: "Đóng",
  pin: "Ghim",
  unpin: "Bỏ ghim",
  mute: "Tắt thông báo",
  unmute: "Bật thông báo",
  deleteChat: "Xóa cuộc trò chuyện",
  deleteChatConfirm: "Xóa cuộc trò chuyện? Chỉ xóa tin nhắn trên thiết bị này.",
  newChat: "Trò chuyện mới",
  ok: "OK",
  readByFooterPartial: "{0} thành viên · {1} người chưa xem",
  readByFooterAll: "Cả {0} thành viên đã xem",
  emoji: "Biểu tượng cảm xúc",
  recalledPreview: "Tin nhắn đã bị thu hồi",
  recalledByYou: "Bạn đã thu hồi một tin nhắn",
  recalledBy: "{0} đã thu hồi một tin nhắn",
  searchMessages: "Tin nhắn",
  noResults: "Không có kết quả",
  searchChats: "Tìm cuộc trò chuyện và tin nhắn",
};

export const builtInStrings = { en, zh, vi } as const;

/** Fills `{0}`, `{1}` … */
export function format(template: string, ...args: (string | number)[]): string {
  return template.replace(/\{(\d+)\}/g, (m, i: string) => (args[Number(i)] !== undefined ? String(args[Number(i)]) : m));
}

/** The built-in table for a BCP 47 language tag; English when the language is not built in. */
export function stringsFor(locale: string): ImStrings {
  const lang = locale.toLowerCase().split("-")[0];
  return lang === "zh" ? zh : lang === "vi" ? vi : en;
}
