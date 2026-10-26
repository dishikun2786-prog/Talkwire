# Generates talkwire-ui's string resources in three languages from one table,
# so the keys can never drift apart. name: (en, zh, vi)
S = {
 "talkwire_preview_image": ("[Image]", "[图片]", "[Hình ảnh]"),
 "talkwire_preview_video": ("[Video]", "[视频]", "[Video]"),
 "talkwire_preview_voice": ("[Voice]", "[语音]", "[Tin nhắn thoại]"),
 "talkwire_preview_file": ("[File] %1$s", "[文件] %1$s", "[Tệp] %1$s"),
 "talkwire_preview_unsupported": ("[Unsupported message]", "[暂不支持的消息]", "[Tin nhắn chưa được hỗ trợ]"),
 "talkwire_unsupported_message": ("This message type isn't supported yet", "暂不支持显示这类消息", "Chưa hỗ trợ hiển thị loại tin nhắn này"),
 "talkwire_image": ("Image", "图片", "Hình ảnh"),
 "talkwire_play_video": ("Play video", "播放视频", "Phát video"),
 "talkwire_close": ("Close", "关闭", "Đóng"),
 "talkwire_video_failed": ("Can't play this video: %1$s", "视频无法播放：%1$s", "Không thể phát video: %1$s"),
 "talkwire_loading": ("Loading…", "加载中…", "Đang tải…"),
 "talkwire_voice_seconds": ("%1$d″", "%1$d″", "%1$d″"),
 "talkwire_playback_speed": ("Speed %1$s", "倍速 %1$s", "Tốc độ %1$s"),
 "talkwire_file": ("File", "文件", "Tệp"),
 # composer
 "talkwire_type_message": ("Message", "发消息", "Nhắn tin"),
 "talkwire_send": ("Send", "发送", "Gửi"),
 "talkwire_hold_to_talk": ("Hold to talk", "按住 说话", "Nhấn giữ để nói"),
 "talkwire_release_to_send": ("Release to send", "松开 发送", "Thả ra để gửi"),
 "talkwire_keyboard": ("Keyboard", "键盘", "Bàn phím"),
 "talkwire_voice_input": ("Voice message", "语音消息", "Tin nhắn thoại"),
 "talkwire_emoji": ("Emoji", "表情", "Biểu tượng cảm xúc"),
 "talkwire_attach": ("More", "更多", "Thêm"),
 "talkwire_too_long": ("Messages can have up to %1$d characters", "消息最多 %1$d 个字", "Tin nhắn tối đa %1$d ký tự"),
 "talkwire_replying_to": ("Reply to %1$s", "回复 %1$s", "Trả lời %1$s"),
 "talkwire_you": ("You", "我", "Bạn"),
 "talkwire_cancel": ("Cancel", "取消", "Hủy"),
 # voice
 "talkwire_release_to_cancel": ("Release to cancel", "松开手指，取消发送", "Thả ra để hủy"),
 "talkwire_recording_elapsed": ("Recording %1$d s of %2$d s", "正在录音 %1$d/%2$d 秒", "Đang ghi %1$d/%2$d giây"),
 "talkwire_swipe_up_cancel": ("Slide up to cancel", "上滑取消", "Vuốt lên để hủy"),
 "talkwire_recording": ("Recording", "正在录音", "Đang ghi âm"),
 "talkwire_record_too_short": ("Too short to send", "说话时间太短", "Quá ngắn để gửi"),
 "talkwire_mic_unavailable": ("Can't use the microphone right now", "暂时无法使用麦克风", "Hiện không thể dùng micrô"),
 "talkwire_mic_permission": ("Allow microphone access to send voice messages", "需要麦克风权限才能发送语音", "Cần quyền micrô để gửi tin nhắn thoại"),
 # list / row
 "talkwire_new_messages": ("New messages", "以下为新消息", "Tin nhắn mới"),
 "talkwire_today": ("Today", "今天", "Hôm nay"),
 "talkwire_yesterday": ("Yesterday", "昨天", "Hôm qua"),
 "talkwire_jump_to_bottom": ("Back to latest", "回到最新", "Về tin mới nhất"),
 "talkwire_forwarded": ("Forwarded", "转发的消息", "Đã chuyển tiếp"),
 "talkwire_sending": ("Sending", "发送中", "Đang gửi"),
 "talkwire_sent": ("Sent", "已发送", "Đã gửi"),
 "talkwire_read": ("Read", "已读", "Đã xem"),
 "talkwire_send_failed_retry": ("Not sent. Tap to retry", "发送失败，点按重发", "Chưa gửi được. Nhấn để thử lại"),
 "talkwire_send_failed": ("Not sent", "发送失败", "Gửi không thành công"),
 "talkwire_read_by_count": ("Read by %1$d", "%1$d 人已读", "%1$d người đã xem"),
 "talkwire_read_by_footer_partial": ("%1$d members · %2$d haven't read it", "共 %1$d 人 · 还有 %2$d 人未读", "%1$d thành viên · %2$d người chưa xem"),
 "talkwire_read_by_footer_all": ("All %1$d members have read it", "共 %1$d 人，全部已读", "Cả %1$d thành viên đã xem"),
 "talkwire_ok": ("OK", "确定", "OK"),
 "talkwire_avatar": ("Avatar", "头像", "Ảnh đại diện"),
 "talkwire_typing": ("Typing…", "对方正在输入…", "Đang nhập…"),
 "talkwire_typing_named": ("%1$s is typing…", "%1$s 正在输入…", "%1$s đang nhập…"),
 "talkwire_online": ("Online", "在线", "Trực tuyến"),
 "talkwire_last_seen_minutes": ("Last seen %1$d min ago", "%1$d 分钟前在线", "Hoạt động %1$d phút trước"),
 "talkwire_last_seen_hours": ("Last seen %1$d h ago", "%1$d 小时前在线", "Hoạt động %1$d giờ trước"),
 "talkwire_last_seen_days": ("Last seen %1$d d ago", "%1$d 天前在线", "Hoạt động %1$d ngày trước"),
 "talkwire_connecting": ("Connecting…", "连接中…", "Đang kết nối…"),
 "talkwire_offline_banner": ("No connection. Messages will be sent when you're back online.", "网络未连接，恢复后会自动发送", "Mất kết nối. Tin nhắn sẽ được gửi khi có mạng."),
 "talkwire_load_older_failed": ("Couldn't load earlier messages", "加载更早的消息失败", "Không tải được tin nhắn cũ hơn"),
 # actions
 "talkwire_action_copy": ("Copy", "复制", "Sao chép"),
 "talkwire_action_save": ("Save", "保存", "Lưu"),
 "talkwire_action_share": ("Share", "分享", "Chia sẻ"),
 "talkwire_action_reply": ("Reply", "回复", "Trả lời"),
 "talkwire_action_forward": ("Forward", "转发", "Chuyển tiếp"),
 "talkwire_action_select": ("Select", "多选", "Chọn nhiều"),
 "talkwire_action_recall": ("Unsend", "撤回", "Thu hồi"),
 "talkwire_action_retry": ("Send again", "重新发送", "Gửi lại"),
 "talkwire_action_discard": ("Delete", "删除", "Xóa"),
 "talkwire_copied": ("Copied", "已复制", "Đã sao chép"),
 "talkwire_saved": ("Saved", "已保存", "Đã lưu"),
 "talkwire_save_failed": ("Couldn't save", "保存失败", "Không lưu được"),
 "talkwire_recall_failed": ("Couldn't unsend this message", "撤回失败", "Không thu hồi được tin nhắn"),
 "talkwire_action_failed": ("Something went wrong. Please try again.", "操作失败，请重试", "Đã xảy ra lỗi. Vui lòng thử lại."),
 # forward
 "talkwire_forward_to": ("Forward to", "转发给", "Chuyển tiếp đến"),
 "talkwire_no_chats": ("No chats yet", "还没有会话", "Chưa có cuộc trò chuyện"),
 "talkwire_send_count": ("Send (%1$d)", "发送（%1$d）", "Gửi (%1$d)"),
 "talkwire_forwarded_ok": ("Forwarded", "已转发", "Đã chuyển tiếp"),
 "talkwire_forward_failed": ("Couldn't forward to %1$d chats", "%1$d 个会话转发失败", "Không chuyển tiếp được đến %1$d cuộc trò chuyện"),
 "talkwire_selected_count": ("%1$d selected", "已选 %1$d 条", "Đã chọn %1$d"),
 # attachments
 "talkwire_attach_photo": ("Photos", "相册", "Ảnh"),
 "talkwire_attach_camera": ("Camera", "拍摄", "Máy ảnh"),
 "talkwire_attach_file": ("File", "文件", "Tệp"),
 "talkwire_too_large": ("Files can be up to %1$d MB", "文件不能超过 %1$d MB", "Tệp tối đa %1$d MB"),
 "talkwire_send_media_failed": ("Couldn't send it. Please try again.", "发送失败，请重试", "Không gửi được. Vui lòng thử lại."),
 "talkwire_no_media": ("This app can't send files", "当前应用不支持发送文件", "Ứng dụng này không gửi được tệp"),
 # conversation list
 "talkwire_chats": ("Chats", "聊天", "Trò chuyện"),
 "talkwire_pin": ("Pin", "置顶", "Ghim"),
 "talkwire_unpin": ("Unpin", "取消置顶", "Bỏ ghim"),
 "talkwire_mute": ("Mute", "免打扰", "Tắt thông báo"),
 "talkwire_unmute": ("Unmute", "取消免打扰", "Bật thông báo"),
 "talkwire_delete_chat": ("Delete chat", "删除聊天", "Xóa cuộc trò chuyện"),
 "talkwire_delete_chat_confirm": ("Delete this chat? Its messages are removed from this device only.", "删除该聊天？只会清除本机上的记录。", "Xóa cuộc trò chuyện? Chỉ xóa tin nhắn trên thiết bị này."),
 "talkwire_draft": ("[Draft] %1$s", "[草稿] %1$s", "[Bản nháp] %1$s"),
 "talkwire_just_now": ("Now", "刚刚", "Vừa xong"),
 "talkwire_minutes_ago": ("%1$d min", "%1$d 分钟前", "%1$d phút"),
 "talkwire_hours_ago": ("%1$d h", "%1$d 小时前", "%1$d giờ"),
 "talkwire_new_chat": ("New chat", "发起聊天", "Trò chuyện mới"),
 "talkwire_new_group": ("New group", "发起群聊", "Nhóm mới"),
 "talkwire_no_conversations": ("No chats yet. Start one from your contacts.", "还没有聊天，从通讯录开始吧", "Chưa có cuộc trò chuyện. Hãy bắt đầu từ danh bạ."),
 "talkwire_sender_preview": ("%1$s: %2$s", "%1$s：%2$s", "%1$s: %2$s"),
 # contacts
 "talkwire_contacts": ("Contacts", "通讯录", "Danh bạ"),
 "talkwire_no_contacts": ("No contacts", "暂无联系人", "Chưa có liên hệ"),
 "talkwire_search": ("Search", "搜索", "Tìm kiếm"),
 # group
 "talkwire_group_info": ("Group info", "群聊信息", "Thông tin nhóm"),
 "talkwire_chat_info": ("Chat info", "聊天信息", "Thông tin trò chuyện"),
 "talkwire_group_name": ("Group name", "群聊名称", "Tên nhóm"),
 "talkwire_rename_group": ("Rename group", "修改群名", "Đổi tên nhóm"),
 "talkwire_members_count": ("Members (%1$d)", "群成员（%1$d）", "Thành viên (%1$d)"),
 "talkwire_add_members": ("Add members", "添加成员", "Thêm thành viên"),
 "talkwire_remove_member": ("Remove from group", "移出群聊", "Xóa khỏi nhóm"),
 "talkwire_make_admin": ("Make admin", "设为管理员", "Đặt làm quản trị viên"),
 "talkwire_remove_admin": ("Remove as admin", "取消管理员", "Bỏ quyền quản trị viên"),
 "talkwire_transfer_owner": ("Make owner", "转让群主", "Chuyển quyền trưởng nhóm"),
 "talkwire_leave_group": ("Leave group", "退出群聊", "Rời nhóm"),
 "talkwire_disband_group": ("Disband group", "解散群聊", "Giải tán nhóm"),
 "talkwire_clear_history": ("Clear chat history", "清空聊天记录", "Xóa lịch sử trò chuyện"),
 "talkwire_clear_history_confirm": ("Clear the history for everyone in this chat? This can't be undone.", "为聊天中的所有人清空记录？此操作无法撤销。", "Xóa lịch sử cho mọi người trong cuộc trò chuyện? Không thể hoàn tác."),
 "talkwire_role_owner": ("Owner", "群主", "Trưởng nhóm"),
 "talkwire_role_admin": ("Admin", "管理员", "Quản trị viên"),
 "talkwire_members_private": ("Members can't view each other's profiles or add each other.", "群成员之间不可查看资料或添加好友", "Thành viên không thể xem hồ sơ hoặc kết bạn với nhau."),
 "talkwire_leave_confirm": ("Leave this group?", "确定退出群聊？", "Rời khỏi nhóm này?"),
 "talkwire_disband_confirm": ("Disband this group? Everyone will be removed.", "确定解散群聊？所有成员都将被移出。", "Giải tán nhóm? Mọi người sẽ bị xóa khỏi nhóm."),
 "talkwire_remove_confirm": ("Remove %1$s from the group?", "将 %1$s 移出群聊？", "Xóa %1$s khỏi nhóm?"),
 "talkwire_transfer_confirm": ("Make %1$s the owner? You'll become a member.", "将群主转让给 %1$s？你将成为普通成员。", "Chuyển quyền trưởng nhóm cho %1$s? Bạn sẽ trở thành thành viên."),
 "talkwire_admin_limit": ("This group already has as many admins as it can have", "管理员人数已达上限", "Nhóm đã có số quản trị viên tối đa"),
 "talkwire_create_group": ("New group", "创建群聊", "Tạo nhóm"),
 "talkwire_create_count": ("Create (%1$d)", "创建（%1$d）", "Tạo (%1$d)"),
 "talkwire_add_count": ("Add (%1$d)", "添加（%1$d）", "Thêm (%1$d)"),
 "talkwire_save": ("Save", "保存", "Lưu"),
 "talkwire_back": ("Back", "返回", "Quay lại"),
 "talkwire_more": ("More", "更多", "Thêm"),
 "talkwire_removed_from_group": ("You're no longer in this group", "你已不在该群聊中", "Bạn không còn trong nhóm này"),
 "talkwire_history_cleared": ("Chat history was cleared", "聊天记录已被清空", "Lịch sử trò chuyện đã bị xóa"),
 "talkwire_mute_notifications": ("Mute notifications", "消息免打扰", "Tắt thông báo"),
 "talkwire_pin_chat": ("Pin chat", "置顶聊天", "Ghim cuộc trò chuyện"),
 "talkwire_member_fallback": ("Member", "群成员", "Thành viên"),
}

import pathlib
import sys


def esc(s):
    s = s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
    s = s.replace("\\", "\\\\").replace("'", "\\'").replace('"', '\\"')
    if s.startswith("@") or s.startswith("?"):
        s = "\\" + s
    return s


root = pathlib.Path(sys.argv[1])
for idx, d in enumerate(["values", "values-zh", "values-vi"]):
    out = ['<?xml version="1.0" encoding="utf-8"?>', "<resources>"]
    for k, v in S.items():
        out.append(f'    <string name="{k}">{esc(v[idx])}</string>')
    out.append("</resources>")
    p = root / d / "talkwire_strings.xml"
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(("\n".join(out) + "\n").encode("utf-8"))
print(len(S), "strings")
