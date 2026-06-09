-- ============================================
-- GK STB Agent ACL — 自定义设备认证
-- ============================================

-- 允许的 MQTT 客户端凭证白名单 (username -> password)
-- 格式: whitelist["username"] = "password"
local whitelist = {
    ["gk-admin"] = "gk-stb-2024",
}

-- 允许的 topic 前缀
local allowed_publish = {
    "devices/",
    "$SYS/",
}

local allowed_subscribe = {
    "devices/",
    "$SYS/",
}

--- 认证钩子: 连接时验证用户名密码
on_client_register = function({username, password, clientid, ipaddr})
    if not username then
        return false
    end

    -- 检查白名单
    local expected_pass = whitelist[username]
    if not expected_pass then
        return false
    end
    if password ~= expected_pass then
        return false
    end

    -- 认证成功，标记角色
    return {
        ok = true,
        user = {
            username = username,
            tags = { role = "device" },
        }
    }
end

--- ACL 发布检查
on_check_publish = function({user, ipaddr, topic, qos, timestamp})
    if not user then
        return false
    end

    local role = user.tags and user.tags.role

    -- 设备只能发布 devices/# 和 $SYS/#
    if role == "device" then
        for _, prefix in ipairs(allowed_publish) do
            if string.sub(topic, 1, #prefix) == prefix then
                return true
            end
        end
        return false
    end

    return true
end

--- ACL 订阅检查
on_check_subscribe = function({user, ipaddr, topic, qos, timestamp})
    if not user then
        return false
    end

    local role = user.tags and user.tags.role

    -- 设备只能订阅 devices/# 和 $SYS/#
    if role == "device" then
        for _, prefix in ipairs(allowed_subscribe) do
            if string.sub(topic, 1, #prefix) == prefix then
                return true
            end
        end
        return false
    end

    return true
end
