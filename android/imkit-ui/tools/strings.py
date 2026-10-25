# Generates imkit-ui's string resources in three languages from one table,
# so the keys can never drift apart. name: (en, zh, vi)
S = {
 "imkit_preview_image": ("[Image]", "[图片]", "[Hình ảnh]"),
 "imkit_preview_video": ("[Video]", "[视频]", "[Video]"),
 "imkit_preview_voice": ("[Voice]", "[语音]", "[Tin nhắn thoại]"),
 "imkit_preview_file": ("[File] %1$s", "[文件] %1$s", "[Tệp] %1$s"),
 "imkit_preview_unsupported": ("[Unsupported message]", "[暂不支持的消息]", "[Tin nhắn chưa được hỗ trợ]"),
 "imkit_unsupported_message": ("This message type isn't supported yet", "暂不支持显示这类消息", "Chưa hỗ trợ hiển thị loại tin nhắn này"),
 "imkit_image": ("Image", "图片", "Hình ảnh"),
 "imkit_play_video": ("Play video", "播放视频", "Phát video"),
 "imkit_close": ("Close", "关闭", "Đóng"),
 "imkit_video_failed": ("Can't play this video: %1$s", "视频无法播放：%1$s", "Không thể phát video: %1$s"),
 "imkit_loading": ("Loading…", "加载中…", "Đang tải…"),
 "imkit_voice_seconds": ("%1$d″", "%1$d″", "%1$d″"),
 "imkit_playback_speed": ("Speed %1$s", "倍速 %1$s", "Tốc độ %1$s"),
 "imkit_file": ("File", "文件", "Tệp"),
 # composer
 "imkit_type_message": ("Message", "发消息", "Nhắn tin"),
 "imkit_send": ("Send", "发送", "Gửi"),
 "imkit_hold_to_talk": ("Hold to talk", "按住 说话", "Nhấn giữ để nói"),
 "imkit_release_to_send": ("Release to send", "松开 发送", "Thả ra để gửi"),
 "imkit_keyboard": ("Keyboard", "键盘", "Bàn phím"),
 "imkit_voice_input": ("Voice message", "语音消息", "Tin nhắn thoại"),
 "imkit_emoji": ("Emoji", "表情", "Biểu tượng cảm xúc"),
 "imkit_attach": ("More", "更多", "Thêm"),
 "imkit_too_long": ("Messages can have up to %1$d characters", "消息最多 %1$d 个字", "Tin nhắn tối đa %1$d ký tự"),
 "imkit_replying_to": ("Reply to %1$s", "回复 %1$s", "Trả lời %1$s"),
 "imkit_you": ("You", "我", "Bạn"),
 "imkit_cancel": ("Cancel", "取消", "Hủy"),
 # voice
 "imkit_release_to_cancel": ("Release to cancel", "松开手指，取消发送", "Thả ra để hủy"),
 "imkit_recording_elapsed": ("Recording %1$d s of %2$d s", "正在录音 %1$d/%2$d 秒", "Đang ghi %1$d/%2$d giây"),
 "imkit_swipe_up_cancel": ("Slide up to cancel", "上滑取消", "Vuốt lên để hủy"),
 "imkit_recording": ("Recording", "正在录音", "Đang ghi âm"),
 "imkit_record_too_short": ("Too short to send", "说话时间太短", "Quá ngắn để gửi"),
 "imkit_mic_unavailable": ("Can't use the microphone right now", "暂时无法使用麦克风", "Hiện không thể dùng micrô"),
 "imkit_mic_permission": ("Allow microphone access to send voice messages", "需要麦克风权限才能发送语音", "Cần quyền micrô để gửi tin nhắn thoại"),
 # list / row
 "imkit_new_messages": ("New messages", "以下为新消息", "Tin nhắn mới"),
 "imkit_today": ("Today", "今天", "Hôm nay"),
 "imkit_yesterday": ("Yesterday", "昨天", "Hôm qua"),
 "imkit_jump_to_bottom": ("Back to latest", "回到最新", "Về tin mới nhất"),
 "imkit_forwarded": ("Forwarded", "转发的消息", "Đã chuyển tiếp"),
 "imkit_sending": ("Sending", "发送中", "Đang gửi"),
 "imkit_sent": ("Sent", "已发送", "Đã gửi"),
 "imkit_read": ("Read", "已读", "Đã xem"),
 "imkit_send_failed_retry": ("Not sent. Tap to retry", "发送失败，点按重发", "Chưa gửi được. Nhấn để thử lại"),
 "imkit_send_failed": ("Not sent", "发送失败", "Gửi không thành công"),
 "imkit_read_by_count": ("Read by %1$d", "%1$d 人已读", "%1$d người đã xem"),
 "imkit_read_by_footer_partial": ("%1$d members · %2$d haven't read it", "共 %1$d 人 · 还有 %2$d 人未读", "%1$d thành viên · %2$d người chưa xem"),
 "imkit_read_by_footer_all": ("All %1$d members have read it", "共 %1$d 人，全部已读", "Cả %1$d thành viên đã xem"),
 "imkit_ok": ("OK", "确定", "OK"),
 "imkit_avatar": ("Avatar", "头像", "Ảnh đại diện"),
 "imkit_typing": ("Typing…", "对方正在输入…", "Đang nhập…"),
 "imkit_typing_named": ("%1$s is typing…", "%1$s 正在输入…", "%1$s đang nhập…"),
 "imkit_online": ("Online", "在线", "Trực tuyến"),
 "imkit_last_seen_minutes": ("Last seen %1$d min ago", "%1$d 分钟前在线", "Hoạt động %1$d phút trước"),
 "imkit_last_seen_hours": ("Last seen %1$d h ago", "%1$d 小时前在线", "Hoạt động %1$d giờ trước"),
 "imkit_last_seen_days": ("Last seen %1$d d ago", "%1$d 天前在线", "Hoạt động %1$d ngày trước"),
 "imkit_connecting": ("Connecting…", "连接中…", "Đang kết nối…"),
 "imkit_offline_banner": ("No connection. Messages will be sent when you're back online.", "网络未连接，恢复后会自动发送", "Mất kết nối. Tin nhắn sẽ được gửi khi có mạng."),
 "imkit_load_older_failed": ("Couldn't load earlier messages", "加载更早的消息失败", "Không tải được tin nhắn cũ hơn"),
 # actions
 "imkit_action_copy": ("Copy", "复制", "Sao chép"),
 "imkit_action_save": ("Save", "保存", "Lưu"),
 "imkit_action_share": ("Share", "分享", "Chia sẻ"),
 "imkit_action_reply": ("Reply", "回复", "Trả lời"),
 "imkit_action_forward": ("Forward", "转发", "Chuyển tiếp"),
 "imkit_action_select": ("Select", "多选", "Chọn nhiều"),
 "imkit_action_recall": ("Unsend", "撤回", "Thu hồi"),
 "imkit_action_retry": ("Send again", "重新发送", "Gửi lại"),
 "imkit_action_discard": ("Delete", "删除", "Xóa"),
 "imkit_copied": ("Copied", "已复制", "Đã sao chép"),
 "imkit_saved": ("Saved", "已保存", "Đã lưu"),
 "imkit_save_failed": ("Couldn't save", "保存失败", "Không lưu được"),
 "imkit_recall_failed": ("Couldn't unsend this message", "撤回失败", "Không thu hồi được tin nhắn"),
 "imkit_action_failed": ("Something went wrong. Please try again.", "操作失败，请重试", "Đã xảy ra lỗi. Vui lòng thử lại."),
 # forward
 "imkit_forward_to": ("Forward to", "转发给", "Chuyển tiếp đến"),
 "imkit_no_chats": ("No chats yet", "还没有会话", "Chưa có cuộc trò chuyện"),
 "imkit_send_count": ("Send (%1$d)", "发送（%1$d）", "Gửi (%1$d)"),
 "imkit_forwarded_ok": ("Forwarded", "已转发", "Đã chuyển tiếp"),
 "imkit_forward_failed": ("Couldn't forward to %1$d chats", "%1$d 个会话转发失败", "Không chuyển tiếp được đến %1$d cuộc trò chuyện"),
 "imkit_selected_count": ("%1$d selected", "已选 %1$d 条", "Đã chọn %1$d"),
 # attachments
 "imkit_attach_photo": ("Photos", "相册", "Ảnh"),
 "imkit_attach_camera": ("Camera", "拍摄", "Máy ảnh"),
 "imkit_attach_file": ("File", "文件", "Tệp"),
 "imkit_too_large": ("Files can be up to %1$d MB", "文件不能超过 %1$d MB", "Tệp tối đa %1$d MB"),
 "imkit_send_media_failed": ("Couldn't send it. Please try again.", "发送失败，请重试", "Không gửi được. Vui lòng thử lại."),
 "imkit_no_media": ("This app can't send files", "当前应用不支持发送文件", "Ứng dụng này không gửi được tệp"),
 # conversation list
 "imkit_chats": ("Chats", "聊天", "Trò chuyện"),
 "imkit_pin": ("Pin", "置顶", "Ghim"),
 "imkit_unpin": ("Unpin", "取消置顶", "Bỏ ghim"),
 "imkit_mute": ("Mute", "免打扰", "Tắt thông báo"),
 "imkit_unmute": ("Unmute", "取消免打扰", "Bật thông báo"),
 "imkit_delete_chat": ("Delete chat", "删除聊天", "Xóa cuộc trò chuyện"),
 "imkit_delete_chat_confirm": ("Delete this chat? Its messages are removed from this device only.", "删除该聊天？只会清除本机上的记录。", "Xóa cuộc trò chuyện? Chỉ xóa tin nhắn trên thiết bị này."),
 "imkit_draft": ("[Draft] %1$s", "[草稿] %1$s", "[Bản nháp] %1$s"),
 "imkit_just_now": ("Now", "刚刚", "Vừa xong"),
 "imkit_minutes_ago": ("%1$d min", "%1$d 分钟前", "%1$d phút"),
 "imkit_hours_ago": ("%1$d h", "%1$d 小时前", "%1$d giờ"),
 "imkit_new_chat": ("New chat", "发起聊天", "Trò chuyện mới"),
 "imkit_new_group": ("New group", "发起群聊", "Nhóm mới"),
 "imkit_no_conversations": ("No chats yet. Start one from your contacts.", "还没有聊天，从通讯录开始吧", "Chưa có cuộc trò chuyện. Hãy bắt đầu từ danh bạ."),
 "imkit_sender_preview": ("%1$s: %2$s", "%1$s：%2$s", "%1$s: %2$s"),
 # contacts
 "imkit_contacts": ("Contacts", "通讯录", "Danh bạ"),
 "imkit_no_contacts": ("No contacts", "暂无联系人", "Chưa có liên hệ"),
 "imkit_search": ("Search", "搜索", "Tìm kiếm"),
 # group
 "imkit_group_info": ("Group info", "群聊信息", "Thông tin nhóm"),
 "imkit_chat_info": ("Chat info", "聊天信息", "Thông tin trò chuyện"),
 "imkit_group_name": ("Group name", "群聊名称", "Tên nhóm"),
 "imkit_rename_group": ("Rename group", "修改群名", "Đổi tên nhóm"),
 "imkit_members_count": ("Members (%1$d)", "群成员（%1$d）", "Thành viên (%1$d)"),
 "imkit_add_members": ("Add members", "添加成员", "Thêm thành viên"),
 "imkit_remove_member": ("Remove from group", "移出群聊", "Xóa khỏi nhóm"),
 "imkit_make_admin": ("Make admin", "设为管理员", "Đặt làm quản trị viên"),
 "imkit_remove_admin": ("Remove as admin", "取消管理员", "Bỏ quyền quản trị viên"),
 "imkit_transfer_owner": ("Make owner", "转让群主", "Chuyển quyền trưởng nhóm"),
 "imkit_leave_group": ("Leave group", "退出群聊", "Rời nhóm"),
 "imkit_disband_group": ("Disband group", "解散群聊", "Giải tán nhóm"),
 "imkit_clear_history": ("Clear chat history", "清空聊天记录", "Xóa lịch sử trò chuyện"),
 "imkit_clear_history_confirm": ("Clear the history for everyone in this chat? This can't be undone.", "为聊天中的所有人清空记录？此操作无法撤销。", "Xóa lịch sử cho mọi người trong cuộc trò chuyện? Không thể hoàn tác."),
 "imkit_role_owner": ("Owner", "群主", "Trưởng nhóm"),
 "imkit_role_admin": ("Admin", "管理员", "Quản trị viên"),
 "imkit_members_private": ("Members can't view each other's profiles or add each other.", "群成员之间不可查看资料或添加好友", "Thành viên không thể xem hồ sơ hoặc kết bạn với nhau."),
 "imkit_leave_confirm": ("Leave this group?", "确定退出群聊？", "Rời khỏi nhóm này?"),
 "imkit_disband_confirm": ("Disband this group? Everyone will be removed.", "确定解散群聊？所有成员都将被移出。", "Giải tán nhóm? Mọi người sẽ bị xóa khỏi nhóm."),
 "imkit_remove_confirm": ("Remove %1$s from the group?", "将 %1$s 移出群聊？", "Xóa %1$s khỏi nhóm?"),
 "imkit_transfer_confirm": ("Make %1$s the owner? You'll become a member.", "将群主转让给 %1$s？你将成为普通成员。", "Chuyển quyền trưởng nhóm cho %1$s? Bạn sẽ trở thành thành viên."),
 "imkit_admin_limit": ("This group already has as many admins as it can have", "管理员人数已达上限", "Nhóm đã có số quản trị viên tối đa"),
 "imkit_create_group": ("New group", "创建群聊", "Tạo nhóm"),
 "imkit_create_count": ("Create (%1$d)", "创建（%1$d）", "Tạo (%1$d)"),
 "imkit_add_count": ("Add (%1$d)", "添加（%1$d）", "Thêm (%1$d)"),
 "imkit_save": ("Save", "保存", "Lưu"),
 "imkit_back": ("Back", "返回", "Quay lại"),
 "imkit_more": ("More", "更多", "Thêm"),
 "imkit_removed_from_group": ("You're no longer in this group", "你已不在该群聊中", "Bạn không còn trong nhóm này"),
 "imkit_history_cleared": ("Chat history was cleared", "聊天记录已被清空", "Lịch sử trò chuyện đã bị xóa"),
 "imkit_mute_notifications": ("Mute notifications", "消息免打扰", "Tắt thông báo"),
 "imkit_pin_chat": ("Pin chat", "置顶聊天", "Ghim cuộc trò chuyện"),
 "imkit_member_fallback": ("Member", "群成员", "Thành viên"),
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
    p = root / d / "imkit_strings.xml"
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(("\n".join(out) + "\n").encode("utf-8"))
print(len(S), "strings")
