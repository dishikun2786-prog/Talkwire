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
