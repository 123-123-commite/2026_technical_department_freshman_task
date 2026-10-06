from playwright.sync_api import expect, sync_playwright
from time import time_ns
import os
import psycopg


def age_post(database_url, author_username, item_name):
    with psycopg.connect(database_url) as connection:
        connection.execute(
            "UPDATE posts SET approved_at=now()-interval '30 days',freshness_confirmed_at=NULL "
            "WHERE author_id=(SELECT id FROM users WHERE username=%s) AND item_name=%s",
            (author_username, item_name),
        )


def register(page, username, nickname, student_number):
    page.get_by_role("button", name="登录 / 注册").click()
    page.get_by_role("button", name="还没有账号？创建一个").click()
    page.get_by_label("用户名").fill(username)
    page.get_by_label("密码").fill("local-demo-password")
    page.get_by_label("称呼").fill(nickname)
    page.get_by_label("学号").fill(student_number)
    page.get_by_role("button", name="创建账号").click()
    expect(page.locator(".account-actions .user-name")).to_have_text(nickname)


def main():
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        base_url = os.environ.get("HDUHELP_E2E_BASE_URL", "http://127.0.0.1:5174")
        suffix = str(time_ns())[-7:]
        silver_item = "银色校园卡-" + suffix
        notebook_item = "遗失的" + silver_item
        author_context = browser.new_context()
        author = author_context.new_page()
        author.goto(base_url)
        author.wait_for_load_state("networkidle")
        register(author, "e2efinder" + suffix, "拾获同学", "2399" + suffix)
        author.get_by_role("button", name="发布一条信息").click()
        author.get_by_role("button", name="我捡到了").click()
        author.get_by_label("物品名称").fill(silver_item)
        author.get_by_label("描述").fill("卡套背面贴有一枚绿色小叶子贴纸。")
        author.get_by_label("物品类别").fill("校园卡")
        author.get_by_label("大概地点").fill("图书馆一楼")
        author.get_by_label("私密核验特征（选填）").fill("卡套背面刻有 731")
        author.get_by_role("button", name="提交管理员审核").click()
        expect(author.get_by_text("已提交审核，通过后会出现在广场。", exact=True)).to_be_visible()

        admin = browser.new_page()
        admin.goto(base_url)
        admin.wait_for_load_state("networkidle")
        admin.get_by_role("button", name="登录 / 注册").click()
        admin.get_by_label("用户名").fill(os.environ.get("HDUHELP_E2E_ADMIN_USERNAME", "moderator"))
        admin.get_by_label("密码").fill(os.environ["HDUHELP_E2E_ADMIN_PASSWORD"])
        admin.locator(".account-dialog form button").click()
        expect(admin.locator(".account-actions .user-name")).to_have_text("系统管理员")
        admin.reload()
        admin.wait_for_load_state("networkidle")
        expect(admin.get_by_role("button", name="管理工作台")).to_be_visible()
        admin.get_by_role("button", name="管理工作台").click()
        admin.locator(".moderation-card").filter(has_text=silver_item).get_by_role("button", name="通过", exact=True).click()
        expect(admin.get_by_text("内容已审核通过。", exact=True)).to_be_visible()

        claimant = browser.new_page()
        claimant.goto(base_url)
        claimant.wait_for_load_state("networkidle")
        claimant.reload()
        claimant.locator(".post-card").filter(has_text=silver_item).first.click()
        claimant_username = "e2eowner" + suffix
        register(claimant, claimant_username, "认领同学", "2388" + suffix)
        claimant.get_by_label("说说情况").fill("这张卡的卡套确实贴有绿色小叶子。")
        claimant.get_by_label("请描述一项物品特征").fill("卡套背面刻有 731")
        claimant.get_by_label("方便联系你的方式").fill("邮箱 owner@example.test")
        claimant.get_by_role("button", name="提交认领").click()
        expect(claimant.get_by_text("已私密发送给发布者。", exact=True)).to_be_visible()

        author.get_by_role("button", name="我的发布").click()
        author.locator(".own-title", has_text=silver_item).click()
        expect(author.get_by_text("收到的申请")).to_be_visible()
        expect(author.get_by_text("卡套背面刻有 731", exact=True)).to_be_visible()
        author.get_by_role("button", name="编辑内容 / 重新提交").click()
        author.get_by_label("私密核验特征（选填）").fill("")
        author.get_by_role("button", name="提交管理员审核").click()
        expect(author.get_by_text("内容已提交审核。", exact=True)).to_be_visible()
        author.get_by_role("button", name="编辑内容 / 重新提交").click()
        expect(author.get_by_label("私密核验特征（选填）")).to_have_value("")
        author.get_by_role("button", name="关闭").click()

        admin.get_by_role("button", name="失物广场").click()
        admin.get_by_role("button", name="管理工作台").click()
        admin.locator(".moderation-card").filter(has_text=silver_item).get_by_role("button", name="通过并更新").click()
        expect(admin.get_by_text("修改已审核通过并更新公开版本。", exact=True)).to_be_visible()

        claimant.get_by_role("button", name="我的发布").click()
        claimant.get_by_role("button", name="发布新信息").click()
        claimant.get_by_role("button", name="我丢失了").click()
        claimant.get_by_label("物品名称").fill(notebook_item)
        claimant.get_by_label("描述").fill("封面有一条白色横线，内页写着课程笔记。")
        claimant.get_by_label("物品类别").fill("校园卡")
        claimant.get_by_label("大概地点").fill("图书馆一楼")
        claimant.get_by_role("button", name="提交管理员审核").click()
        expect(claimant.get_by_text("已提交审核，通过后会出现在广场。", exact=True)).to_be_visible()

        admin.get_by_role("button", name="失物广场").click()
        admin.get_by_role("button", name="管理工作台").click()
        admin.locator(".moderation-card").filter(has_text=notebook_item).get_by_role("button", name="通过", exact=True).click()
        expect(admin.get_by_text("内容已审核通过。", exact=True)).to_be_visible()

        age_post(os.environ["HDUHELP_E2E_DATABASE_URL"], claimant_username, notebook_item)
        claimant.get_by_role("button", name="失物广场").click()
        claimant.get_by_role("button", name="我的发布").click()
        expect(claimant.get_by_text("公开信息已满 30 天，待你确认")).to_be_visible()
        claimant.get_by_role("button", name="确认仍然有效").click()
        expect(claimant.get_by_text("公开信息已满 30 天，待你确认")).to_have_count(0)

        author.get_by_role("button", name="失物广场").click()
        author.locator(".post-card").filter(has_text=notebook_item).click()
        expect(author.get_by_text("相反类型的相关线索")).to_be_visible()
        expect(author.get_by_text("遗失的" + silver_item, exact=True)).to_be_visible()
        author.get_by_role("button", name="返回信息板").click()
        author.get_by_role("button", name="我的发布").click()
        author.locator(".own-title", has_text=silver_item).click()
        author.get_by_role("button", name="接受申请").click()
        expect(author.locator(".aside-status").get_by_text("已归还")).to_be_visible()
        expect(author.get_by_text("已接受，相关内容状态已更新。", exact=True)).to_be_visible()

        author.get_by_role("button", name="失物广场").click()
        author.locator(".post-card").filter(has_text=notebook_item).click()
        author.get_by_label("说说情况").fill("我在教学楼 C 区看见一本封面有白色横线的笔记本。")
        author.get_by_label("方便联系你的方式").fill("邮箱 finder@example.test")
        author.get_by_role("button", name="发送线索").click()
        expect(author.get_by_text("已私密发送给发布者。", exact=True)).to_be_visible()
        author.get_by_role("button", name="举报此内容").click()
        author.get_by_label("情况说明").fill("请管理员核查这条公开信息。")
        author.get_by_role("button", name="提交举报").click()
        expect(author.get_by_text("举报已提交，管理员会按流程处理。", exact=True)).to_be_visible()

        claimant.get_by_role("button", name="我的发布").click()
        claimant.locator(".own-title", has_text=notebook_item).click()
        expect(claimant.get_by_text("收到的申请")).to_be_visible()
        claimant.get_by_role("button", name="接受申请").click()
        expect(claimant.locator(".aside-status").get_by_text("已找回")).to_be_visible()

        admin.get_by_role("button", name="失物广场").click()
        admin.get_by_role("button", name="管理工作台").click()
        admin.locator(".report-card").filter(has_text="请管理员核查这条公开信息").get_by_role("button", name="驳回举报").click()
        expect(admin.locator(".report-card").get_by_text("已驳回")).to_be_visible()
        print("浏览器流程通过：私密核验 → 线索推荐 → 30 天确认 → 认领结案 → 举报处理")
        browser.close()


if __name__ == "__main__":
    main()
