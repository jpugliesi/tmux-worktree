local plugin = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(plugin)
vim.g.mapleader = " "

require("twt").setup()

for _, mode in ipairs({ "n", "v" }) do
  local short = vim.fn.maparg("<leader>an", mode, false, true)
  local review = vim.fn.maparg("<leader>arn", mode, false, true)
  assert(short.callback, "<leader>an is missing in " .. mode .. " mode")
  assert(review.callback, "<leader>arn is missing in " .. mode .. " mode")
  assert(short.desc == review.desc, "the Review Note aliases have different descriptions")
end

print("twt.nvim keymap tests: ok")
